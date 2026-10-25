// Package compatibilityintegration proves the finite v1/v2 rollback window
// with real service processes and disposable databases. Version 2 DDL and the
// C1 writer exist only in this test; no production migration 2 is shipped.
package compatibilityintegration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	notificationtest "github.com/syuhei0519/backend-app/internal/notificationdockertest"
	"github.com/syuhei0519/backend-app/internal/ordertest"
	paymenttest "github.com/syuhei0519/backend-app/internal/paymentdockertest"
)

type writerIDs struct {
	Account, Order, Payment, Notification int64
}

func helperWriter(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	open := func(key string) *pgxpool.Pool {
		pool, err := pgxpool.New(ctx, os.Getenv(key))
		if err != nil {
			t.Fatal("open test-only C1 writer")
		}
		t.Cleanup(pool.Close)
		return pool
	}
	account, order := open("COMPAT_ACCOUNT_URL"), open("COMPAT_ORDER_URL")
	payment, notification := open("COMPAT_PAYMENT_URL"), open("COMPAT_NOTIFICATION_URL")
	ids := writerIDs{}
	if err := account.QueryRow(ctx, `INSERT INTO accounts(name,email,name_e1) VALUES('C1 dual','c1-dual@example.invalid','C1 dual') RETURNING id`).Scan(&ids.Account); err != nil {
		t.Fatal("C1 Account dual-write")
	}
	if err := order.QueryRow(ctx, `INSERT INTO orders(account_id,item_code,quantity,item_code_e1) VALUES($1,'C1-ORDER',2,'C1-ORDER') RETURNING id`, ids.Account).Scan(&ids.Order); err != nil {
		t.Fatal("C1 Order dual-write")
	}
	if err := payment.QueryRow(ctx, `INSERT INTO payments(request_id,order_id,amount_minor,currency,simulate_result,status,currency_e1) VALUES('550e8400-e29b-41d4-a716-446655440003',$1,1200,'JPY','success','succeeded','JPY') RETURNING id`, ids.Order).Scan(&ids.Payment); err != nil {
		t.Fatal("C1 Payment dual-write")
	}
	tx, err := notification.Begin(ctx)
	if err != nil {
		t.Fatal("C1 Notification transaction")
	}
	defer tx.Rollback(context.Background())
	if err = tx.QueryRow(ctx, `INSERT INTO notifications(request_id,order_id,recipient,message,simulate_result,status,recipient_e1) VALUES('550e8400-e29b-41d4-a716-446655440004',$1,'compat-user','C1 notification','success','sent','compat-user') RETURNING id`, ids.Order).Scan(&ids.Notification); err != nil {
		t.Fatal("C1 Notification dual-write")
	}
	if _, err = tx.Exec(ctx, `INSERT INTO notification_sink_receipts(notification_id,result) VALUES($1,'sent')`, ids.Notification); err != nil {
		t.Fatal("C1 Notification receipt")
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal("commit C1 Notification")
	}
	raw, _ := json.Marshal(ids)
	if err = os.WriteFile(os.Getenv("COMPAT_RESULT_PATH"), raw, 0o600); err != nil {
		t.Fatal("write C1 result")
	}
}

type service struct {
	name, command, binary, databaseURL, addressVariable string
}

type process struct {
	cmd     *exec.Cmd
	done    chan error
	base    string
	stopped bool
}

func freeAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("allocate compatibility HTTP port")
	}
	address := listener.Addr().String()
	_ = listener.Close()
	return address
}

func serviceEnvironment(s service, address, policy, accountURL string) []string {
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=/tmp", "SYSTEMROOT=" + os.Getenv("SYSTEMROOT"),
		"DATABASE_URL=" + s.databaseURL, s.addressVariable + "=" + address, "LOG_LEVEL=error"}
	if policy != "" {
		env = append(env, "SCHEMA_COMPATIBILITY_PROFILE="+policy)
	}
	if s.name == "order" {
		env = append(env, "ORDER_ACCOUNT_URL="+accountURL)
	}
	return env
}

func start(t *testing.T, ctx context.Context, s service, policy, accountURL string, ready int) *process {
	t.Helper()
	address := freeAddress(t)
	p := &process{cmd: exec.CommandContext(ctx, s.binary), done: make(chan error, 1), base: "http://" + address}
	p.cmd.Env = serviceEnvironment(s, address, policy, accountURL)
	if err := p.cmd.Start(); err != nil {
		t.Fatal("start " + s.name + " compatibility process")
	}
	go func() { p.done <- p.cmd.Wait() }()
	t.Cleanup(func() {
		if !p.stopped {
			_ = p.cmd.Process.Kill()
			<-p.done
		}
	})
	client := &http.Client{Timeout: 300 * time.Millisecond}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(p.base + "/readyz")
		if err == nil {
			response.Body.Close()
			if response.StatusCode == ready {
				return p
			}
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatal(s.name + " compatibility readiness")
	return nil
}

func stop(t *testing.T, p *process) {
	t.Helper()
	if runtime.GOOS == "windows" {
		_ = p.cmd.Process.Kill()
	} else if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal("signal compatibility process")
	}
	select {
	case err := <-p.done:
		p.stopped = true
		if err != nil {
			t.Fatal("stop compatibility process")
		}
	case <-time.After(20 * time.Second):
		t.Fatal("compatibility process shutdown timeout")
	}
}

func call(t *testing.T, method, url, body string, status int) []byte {
	t.Helper()
	request, err := http.NewRequest(method, url, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal("compatibility request")
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := (&http.Client{Timeout: 4 * time.Second}).Do(request)
	if err != nil {
		t.Fatal("compatibility HTTP transport")
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode != status {
		t.Fatalf("%s expected %d got %d", url, status, response.StatusCode)
	}
	return raw
}

func createdID(t *testing.T, raw []byte) int64 {
	t.Helper()
	var value struct {
		ID int64 `json:"id"`
	}
	if json.Unmarshal(raw, &value) != nil || value.ID < 1 {
		t.Fatal("compatibility create response")
	}
	return value.ID
}

func requireResource(t *testing.T, raw []byte, id int64, field, value string) {
	t.Helper()
	var resource map[string]any
	if json.Unmarshal(raw, &resource) != nil {
		t.Fatal("decode compatibility resource")
	}
	actualID, ok := resource["id"].(float64)
	if !ok || int64(actualID) != id || resource[field] != value {
		t.Fatalf("compatibility resource id=%v %s=%v", resource["id"], field, resource[field])
	}
}

func TestPreparedBinaryRollbackWindow(t *testing.T) {
	if os.Getenv("COMPAT_C1_WRITER_HELPER") == "1" {
		helperWriter(t)
		return
	}
	ordertest.Enabled(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	accountDB := ordertest.Start(t, "accounts", "account_app")
	orderDB := ordertest.Start(t, "orders", "order_app")
	paymentDB := paymenttest.Start(t)
	notificationDB := notificationtest.Start(t)
	tmp := t.TempDir()
	services := []service{
		{"account", "server", "", accountDB.URL(), "ACCOUNT_HTTP_ADDRESS"},
		{"order", "order-server", "", orderDB.URL(), "ORDER_HTTP_ADDRESS"},
		{"payment", "payment-server", "", paymentDB.URL(), "PAYMENT_HTTP_ADDRESS"},
		{"notification", "notification-server", "", notificationDB.URL(), "NOTIFICATION_HTTP_ADDRESS"},
	}
	for index := range services {
		name := services[index].name + "-server"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		services[index].binary = filepath.Join(tmp, name)
		build := exec.CommandContext(ctx, "go", "build", "-o", services[index].binary, "./cmd/"+services[index].command)
		build.Dir = "../.."
		if err := build.Run(); err != nil {
			t.Fatal("build compatibility " + services[index].name)
		}
		migrate := exec.CommandContext(ctx, services[index].binary, "migrate")
		migrate.Env = serviceEnvironment(services[index], "127.0.0.1:0", "", "")
		if err := migrate.Run(); err != nil {
			t.Fatal("migrate compatibility " + services[index].name)
		}
	}
	prepared := "prepared-v1-v2"
	account := start(t, ctx, services[0], prepared, "", http.StatusOK)
	order := start(t, ctx, services[1], prepared, account.base, http.StatusOK)
	payment := start(t, ctx, services[2], prepared, "", http.StatusOK)
	notification := start(t, ctx, services[3], prepared, "", http.StatusOK)
	accountID := createdID(t, call(t, "POST", account.base+"/api/accounts", `{"name":"Prepared","email":"prepared@example.invalid"}`, http.StatusCreated))
	orderID := createdID(t, call(t, "POST", order.base+"/api/orders", fmt.Sprintf(`{"account_id":%d,"item_code":"PREPARED","quantity":1}`, accountID), http.StatusCreated))
	paymentID := createdID(t, call(t, "POST", payment.base+"/api/payments", fmt.Sprintf(`{"request_id":"550e8400-e29b-41d4-a716-446655440001","order_id":%d,"amount_minor":1000,"currency":"JPY","simulate_result":"success"}`, orderID), http.StatusCreated))
	notificationID := createdID(t, call(t, "POST", notification.base+"/api/notifications", fmt.Sprintf(`{"request_id":"550e8400-e29b-41d4-a716-446655440002","order_id":%d,"recipient":"compat-user","message":"prepared notification","simulate_result":"success"}`, orderID), http.StatusCreated))
	for _, p := range []*process{notification, payment, order, account} {
		stop(t, p)
	}

	pools := make([]*pgxpool.Pool, len(services))
	for index, s := range services {
		pool, err := pgxpool.New(ctx, s.databaseURL)
		if err != nil {
			t.Fatal("open compatibility fixture")
		}
		pools[index] = pool
		defer pool.Close()
	}
	expansions := []string{
		`ALTER TABLE accounts ADD COLUMN name_e1 VARCHAR(100); INSERT INTO schema_migrations(version) VALUES(2)`,
		`ALTER TABLE orders ADD COLUMN item_code_e1 VARCHAR(64); INSERT INTO order_schema_migrations(version) VALUES(2)`,
		`ALTER TABLE payments ADD COLUMN currency_e1 TEXT; INSERT INTO payment_schema_migrations(version) VALUES(2)`,
		`ALTER TABLE notifications ADD COLUMN recipient_e1 VARCHAR(64); INSERT INTO notification_schema_migrations(version) VALUES(2)`,
	}
	for index, sql := range expansions {
		if _, err := pools[index].Exec(ctx, sql); err != nil {
			t.Fatal("apply test-only E1 for " + services[index].name)
		}
		migrate := exec.CommandContext(ctx, services[index].binary, "migrate")
		migrate.Env = serviceEnvironment(services[index], "127.0.0.1:0", prepared, "")
		if err := migrate.Run(); err == nil {
			t.Fatal("prepared binary applied an unshipped migration for " + services[index].name)
		}
	}

	strictAccount := start(t, ctx, services[0], "", "", http.StatusServiceUnavailable)
	strictOrder := start(t, ctx, services[1], "", strictAccount.base, http.StatusServiceUnavailable)
	strictPayment := start(t, ctx, services[2], "", "", http.StatusServiceUnavailable)
	strictNotification := start(t, ctx, services[3], "", "", http.StatusServiceUnavailable)
	for _, p := range []*process{strictNotification, strictPayment, strictOrder, strictAccount} {
		stop(t, p)
	}

	account = start(t, ctx, services[0], prepared, "", http.StatusOK)
	order = start(t, ctx, services[1], prepared, account.base, http.StatusOK)
	payment = start(t, ctx, services[2], prepared, "", http.StatusOK)
	notification = start(t, ctx, services[3], prepared, "", http.StatusOK)
	old := []struct {
		base, path, field, value string
		id                       int64
	}{
		{account.base, "/api/accounts/" + strconv.FormatInt(accountID, 10), "name", "Prepared", accountID},
		{order.base, "/api/orders/" + strconv.FormatInt(orderID, 10), "item_code", "PREPARED", orderID},
		{payment.base, "/api/payments/" + strconv.FormatInt(paymentID, 10), "currency", "JPY", paymentID},
		{notification.base, "/api/notifications/" + strconv.FormatInt(notificationID, 10), "recipient", "compat-user", notificationID},
	}
	for _, item := range old {
		requireResource(t, call(t, "GET", item.base+item.path, "", http.StatusOK), item.id, item.field, item.value)
	}
	crudAccountID := createdID(t, call(t, "POST", account.base+"/api/accounts", `{"name":"Prepared CRUD","email":"prepared-crud@example.invalid"}`, http.StatusCreated))
	requireResource(t, call(t, "PUT", account.base+"/api/accounts/"+strconv.FormatInt(crudAccountID, 10), `{"name":"Prepared updated","email":"prepared-updated@example.invalid"}`, http.StatusOK), crudAccountID, "name", "Prepared updated")
	requireResource(t, call(t, "GET", account.base+"/api/accounts/"+strconv.FormatInt(crudAccountID, 10), "", http.StatusOK), crudAccountID, "email", "prepared-updated@example.invalid")
	call(t, "DELETE", account.base+"/api/accounts/"+strconv.FormatInt(crudAccountID, 10), "", http.StatusNoContent)
	call(t, "GET", account.base+"/api/accounts/"+strconv.FormatInt(crudAccountID, 10), "", http.StatusNotFound)
	preparedAccountID := createdID(t, call(t, "POST", account.base+"/api/accounts", `{"name":"Prepared mixed","email":"prepared-mixed@example.invalid"}`, http.StatusCreated))
	preparedOrderID := createdID(t, call(t, "POST", order.base+"/api/orders", fmt.Sprintf(`{"account_id":%d,"item_code":"PREPARED-MIXED","quantity":1}`, preparedAccountID), http.StatusCreated))
	preparedPaymentID := createdID(t, call(t, "POST", payment.base+"/api/payments", fmt.Sprintf(`{"request_id":"550e8400-e29b-41d4-a716-446655440005","order_id":%d,"amount_minor":1100,"currency":"JPY","simulate_result":"success"}`, preparedOrderID), http.StatusCreated))
	preparedNotificationID := createdID(t, call(t, "POST", notification.base+"/api/notifications", fmt.Sprintf(`{"request_id":"550e8400-e29b-41d4-a716-446655440006","order_id":%d,"recipient":"compat-mixed","message":"prepared mixed notification","simulate_result":"success"}`, preparedOrderID), http.StatusCreated))
	preparedRows := []struct {
		base, path, field, value string
		id                       int64
	}{
		{account.base, "/api/accounts/" + strconv.FormatInt(preparedAccountID, 10), "name", "Prepared mixed", preparedAccountID},
		{order.base, "/api/orders/" + strconv.FormatInt(preparedOrderID, 10), "item_code", "PREPARED-MIXED", preparedOrderID},
		{payment.base, "/api/payments/" + strconv.FormatInt(preparedPaymentID, 10), "currency", "JPY", preparedPaymentID},
		{notification.base, "/api/notifications/" + strconv.FormatInt(preparedNotificationID, 10), "recipient", "compat-mixed", preparedNotificationID},
	}

	resultPath := filepath.Join(tmp, "c1-result.json")
	helper := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPreparedBinaryRollbackWindow$")
	helper.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=/tmp", "SYSTEMROOT=" + os.Getenv("SYSTEMROOT"),
		"COMPAT_C1_WRITER_HELPER=1", "COMPAT_RESULT_PATH=" + resultPath,
		"COMPAT_ACCOUNT_URL=" + services[0].databaseURL, "COMPAT_ORDER_URL=" + services[1].databaseURL,
		"COMPAT_PAYMENT_URL=" + services[2].databaseURL, "COMPAT_NOTIFICATION_URL=" + services[3].databaseURL}
	if err := helper.Run(); err != nil {
		t.Fatal("test-only C1 writer process")
	}
	var ids writerIDs
	raw, err := os.ReadFile(resultPath)
	if err != nil || json.Unmarshal(raw, &ids) != nil {
		t.Fatal("read C1 writer evidence")
	}
	newRows := []struct {
		base, path, field, value string
		id                       int64
	}{
		{account.base, "/api/accounts/" + strconv.FormatInt(ids.Account, 10), "name", "C1 dual", ids.Account},
		{order.base, "/api/orders/" + strconv.FormatInt(ids.Order, 10), "item_code", "C1-ORDER", ids.Order},
		{payment.base, "/api/payments/" + strconv.FormatInt(ids.Payment, 10), "currency", "JPY", ids.Payment},
		{notification.base, "/api/notifications/" + strconv.FormatInt(ids.Notification, 10), "recipient", "compat-user", ids.Notification},
	}
	for _, item := range newRows {
		requireResource(t, call(t, "GET", item.base+item.path, "", http.StatusOK), item.id, item.field, item.value)
	}
	for _, item := range preparedRows {
		requireResource(t, call(t, "GET", item.base+item.path, "", http.StatusOK), item.id, item.field, item.value)
	}
	for _, p := range []*process{notification, payment, order, account} {
		stop(t, p)
	}
	account = start(t, ctx, services[0], prepared, "", http.StatusOK)
	order = start(t, ctx, services[1], prepared, account.base, http.StatusOK)
	payment = start(t, ctx, services[2], prepared, "", http.StatusOK)
	notification = start(t, ctx, services[3], prepared, "", http.StatusOK)
	for index, item := range newRows {
		bases := []string{account.base, order.base, payment.base, notification.base}
		requireResource(t, call(t, "GET", bases[index]+item.path, "", http.StatusOK), item.id, item.field, item.value)
	}
	for index, item := range preparedRows {
		bases := []string{account.base, order.base, payment.base, notification.base}
		requireResource(t, call(t, "GET", bases[index]+item.path, "", http.StatusOK), item.id, item.field, item.value)
	}
	for _, p := range []*process{notification, payment, order, account} {
		stop(t, p)
	}
	t.Log("C0 binaries on v1, strict rejection on {1,2}, prepared and test-only C1 mixed writes, rollback reads and four-service restart passed")
}
