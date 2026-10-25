// Payment のデータを専用 PostgreSQL に保存・取得し、接続先と DB エラーの扱いを制限します。
// Package paymentpostgres accesses only the payments database as payment_app.
package paymentpostgres

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/syuhei0519/backend-app/internal/payment"
)

var ErrTarget = errors.New("Payment database target rejected")

type Store struct{ pool *pgxpool.Pool }

func Open(ctx context.Context, url string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, payment.ErrInternal
	}
	// Reject obviously wrong targets before any connection. Verify actual identity
	// on every physical connection as well, including reconnects and migration.
	if cfg.ConnConfig.Database != "payments" || cfg.ConnConfig.User != "payment_app" {
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
		return nil, payment.ErrInternal
	}
	return &Store{pool: pool}, nil
}

type rowQuery interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func guard(ctx context.Context, c rowQuery) error {
	var db, user string
	if err := c.QueryRow(ctx, `SELECT current_database(),current_user`).Scan(&db, &user); err != nil {
		return classify(err)
	}
	if db != "payments" || user != "payment_app" {
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
	var version, count int
	if err := s.pool.QueryRow(ctx, `SELECT COALESCE(MAX(version),0),count(*) FROM public.payment_schema_migrations`).Scan(&version, &count); err != nil {
		return classify(err)
	}
	if version != 1 || count != 1 {
		return payment.ErrUnavailable
	}
	return nil
}

const columns = "id,request_id::text,order_id,amount_minor,currency,simulate_result,status,created_at"

func (s *Store) Create(ctx context.Context, input payment.Input) (payment.Payment, bool, error) {
	if err := payment.Validate(input); err != nil {
		return payment.Payment{}, false, err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	// Separate SELECT after conflict resolution sees the committed winner at READ COMMITTED.
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return payment.Payment{}, false, classify(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = tx.Rollback(cleanup)
	}()
	item, err := scan(tx.QueryRow(ctx, `INSERT INTO public.payments(request_id,order_id,amount_minor,currency,simulate_result,status) VALUES($1,$2,$3,$4,$5,'received') ON CONFLICT(request_id) DO NOTHING RETURNING `+columns, input.RequestID, input.OrderID, input.AmountMinor, input.Currency, input.SimulateResult))
	created := true
	if errors.Is(err, payment.ErrNotFound) {
		created = false
		item, err = scan(tx.QueryRow(ctx, "SELECT "+columns+" FROM public.payments WHERE request_id=$1", input.RequestID))
		if err == nil && item.Input != input {
			return payment.Payment{}, false, payment.ErrConflict
		}
	} else if err == nil {
		item, err = scan(tx.QueryRow(ctx, "UPDATE public.payments SET status=$1 WHERE id=$2 RETURNING "+columns, payment.Result(input), item.ID))
	}
	if err != nil {
		return payment.Payment{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return payment.Payment{}, false, classify(err)
	}
	return item, created, nil
}
func (s *Store) Get(ctx context.Context, id int64) (payment.Payment, error) {
	if id < 1 {
		return payment.Payment{}, payment.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return scan(s.pool.QueryRow(ctx, "SELECT "+columns+" FROM public.payments WHERE id=$1", id))
}
func scan(row pgx.Row) (payment.Payment, error) {
	var item payment.Payment
	if err := row.Scan(&item.ID, &item.RequestID, &item.OrderID, &item.AmountMinor, &item.Currency, &item.SimulateResult, &item.Status, &item.CreatedAt); err != nil {
		return payment.Payment{}, classify(err)
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
		return payment.ErrNotFound
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return payment.ErrUnavailable
	}
	var p *pgconn.PgError
	if errors.As(err, &p) {
		if strings.HasPrefix(p.Code, "08") || strings.HasPrefix(p.Code, "53") || p.Code == "57P01" || p.Code == "57P02" || p.Code == "57P03" || p.Code == "57014" {
			return payment.ErrUnavailable
		}
		return payment.ErrInternal
	}
	var connect *pgconn.ConnectError
	var network net.Error
	if errors.As(err, &connect) || errors.As(err, &network) || pgconn.SafeToRetry(err) || strings.Contains(err.Error(), "closed") {
		return payment.ErrUnavailable
	}
	return payment.ErrInternal
}
