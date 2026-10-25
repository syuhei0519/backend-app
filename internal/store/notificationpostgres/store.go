// Notification のデータを専用 PostgreSQL に保存・取得し、接続先と DB エラーの扱いを制限します。
// Package notificationpostgres accesses only the notifications database as notification_app.
package notificationpostgres

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/syuhei0519/backend-app/internal/notification"
	"github.com/syuhei0519/backend-app/internal/schemahistory"
)

var ErrTarget = errors.New("Notification database target rejected")

type Store struct {
	pool   *pgxpool.Pool
	policy schemahistory.Policy
}

func Open(ctx context.Context, url string) (*Store, error) {
	policy, err := schemahistory.LoadPolicy()
	if err != nil {
		return nil, notification.ErrInternal
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, notification.ErrInternal
	}
	// Reject obviously wrong targets before any connection. Verify actual identity
	// on every physical connection as well, including reconnects and migration.
	if cfg.ConnConfig.Database != "notifications" || cfg.ConnConfig.User != "notification_app" {
		return nil, ErrTarget
	}
	cfg.MinConns = 0
	cfg.MaxConns = 5
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.ConnConfig.ConnectTimeout = 3 * time.Second
	cfg.ConnConfig.RuntimeParams["search_path"] = "pg_catalog,public"
	cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error { return guard(ctx, c) }
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, notification.ErrInternal
	}
	return &Store{pool: pool, policy: policy}, nil
}

type rowQuery interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func guard(ctx context.Context, c rowQuery) error {
	var db, user string
	if err := c.QueryRow(ctx, `SELECT current_database(),current_user`).Scan(&db, &user); err != nil {
		return classify(err)
	}
	if db != "notifications" || user != "notification_app" {
		return ErrTarget
	}
	return nil
}
func (s *Store) Close() { s.pool.Close() }
func (s *Store) CheckTarget(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return guard(ctx, s.pool)
}
func (s *Store) Ready(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if !s.policy.Ready(schemahistory.Inspect(ctx, s.pool, "public.notification_schema_migrations")) {
		return notification.ErrUnavailable
	}
	return nil
}

const columns = "id,request_id::text,order_id,recipient,message,simulate_result,status,created_at"

func (s *Store) Create(ctx context.Context, input notification.Input) (notification.Notification, bool, error) {
	if err := notification.Validate(input); err != nil {
		return notification.Notification{}, false, err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return notification.Notification{}, false, classify(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = tx.Rollback(cleanup)
	}()
	// received exists only inside this transaction; no pending row is published.
	item, err := scan(tx.QueryRow(ctx, `INSERT INTO public.notifications(request_id,order_id,recipient,message,simulate_result,status) VALUES($1,$2,$3,$4,$5,'received') ON CONFLICT(request_id) DO NOTHING RETURNING `+columns, input.RequestID, input.OrderID, input.Recipient, input.Message, input.SimulateResult))
	created := true
	if errors.Is(err, notification.ErrNotFound) {
		created = false
		// Separate statement after INSERT conflict resolution gets a fresh READ COMMITTED snapshot.
		item, err = scan(tx.QueryRow(ctx, `SELECT `+columns+` FROM public.notifications WHERE request_id=$1`, input.RequestID))
		if err == nil && item.Input != input {
			return notification.Notification{}, false, notification.ErrConflict
		}
	} else if err == nil {
		status := notification.Result(input)
		// The durable receipt IS the fake sink. Never invoke it on the replay path.
		if _, err = tx.Exec(ctx, `INSERT INTO public.notification_sink_receipts(notification_id,result) VALUES($1,$2)`, item.ID, status); err != nil {
			return notification.Notification{}, false, classify(err)
		}
		item, err = scan(tx.QueryRow(ctx, `UPDATE public.notifications SET status=$2 WHERE id=$1 RETURNING `+columns, item.ID, status))
	}
	if err != nil {
		return notification.Notification{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return notification.Notification{}, false, classify(err)
	}
	return item, created, nil
}
func (s *Store) Get(ctx context.Context, id int64) (notification.Notification, error) {
	if id < 1 {
		return notification.Notification{}, notification.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return scan(s.pool.QueryRow(ctx, `SELECT `+columns+` FROM public.notifications WHERE id=$1`, id))
}
func scan(row pgx.Row) (notification.Notification, error) {
	var item notification.Notification
	if err := row.Scan(&item.ID, &item.RequestID, &item.OrderID, &item.Recipient, &item.Message, &item.SimulateResult, &item.Status, &item.CreatedAt); err != nil {
		return notification.Notification{}, classify(err)
	}
	item.CreatedAt = item.CreatedAt.UTC()
	return item, nil
}

// Never expose a raw driver error (which can contain SQL, data, or a DSN).
func classify(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return notification.ErrNotFound
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return notification.ErrUnavailable
	}
	var p *pgconn.PgError
	if errors.As(err, &p) {
		if strings.HasPrefix(p.Code, "08") || strings.HasPrefix(p.Code, "53") || p.Code == "57P01" || p.Code == "57P02" || p.Code == "57P03" || p.Code == "57014" {
			return notification.ErrUnavailable
		}
		return notification.ErrInternal
	}
	var connect *pgconn.ConnectError
	var network net.Error
	if errors.As(err, &connect) || errors.As(err, &network) || pgconn.SafeToRetry(err) || strings.Contains(err.Error(), "closed") {
		return notification.ErrUnavailable
	}
	return notification.ErrInternal
}
