// Package orderpostgres accesses only the orders database as order_app.
package orderpostgres

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/syuhei0519/backend-app/internal/order"
)

var ErrTarget = errors.New("Order database target rejected")

type Store struct{ pool *pgxpool.Pool }

func Open(ctx context.Context, url string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, order.ErrInternal
	}
	// Reject obviously wrong targets before any connection. Verify actual identity
	// on every physical connection as well, including reconnects and migration.
	if cfg.ConnConfig.Database != "orders" || cfg.ConnConfig.User != "order_app" {
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
		return nil, order.ErrInternal
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
	if db != "orders" || user != "order_app" {
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
	if err := s.pool.QueryRow(ctx, `SELECT COALESCE(MAX(version),0),count(*) FROM public.order_schema_migrations`).Scan(&version, &count); err != nil {
		return classify(err)
	}
	if version != 1 || count != 1 {
		return order.ErrUnavailable
	}
	return nil
}
func (s *Store) Create(ctx context.Context, input order.Input) (order.Order, error) {
	if err := order.Validate(input); err != nil {
		return order.Order{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	// Explicit commit before returning ensures HTTP 201 cannot precede commit.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return order.Order{}, classify(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = tx.Rollback(cleanup)
	}()
	item, err := scan(tx.QueryRow(ctx, `INSERT INTO public.orders(account_id,item_code,quantity) VALUES($1,$2,$3) RETURNING id,account_id,item_code,quantity,created_at`, input.AccountID, input.ItemCode, input.Quantity))
	if err != nil {
		return order.Order{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return order.Order{}, classify(err)
	}
	return item, nil
}
func (s *Store) Get(ctx context.Context, id int64) (order.Order, error) {
	if id < 1 {
		return order.Order{}, order.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return scan(s.pool.QueryRow(ctx, `SELECT id,account_id,item_code,quantity,created_at FROM public.orders WHERE id=$1`, id))
}
func scan(row pgx.Row) (order.Order, error) {
	var item order.Order
	if err := row.Scan(&item.ID, &item.AccountID, &item.ItemCode, &item.Quantity, &item.CreatedAt); err != nil {
		return order.Order{}, classify(err)
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
		return order.ErrNotFound
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return order.ErrUnavailable
	}
	var p *pgconn.PgError
	if errors.As(err, &p) {
		if strings.HasPrefix(p.Code, "08") || strings.HasPrefix(p.Code, "53") || p.Code == "57P01" || p.Code == "57P02" || p.Code == "57P03" || p.Code == "57014" {
			return order.ErrUnavailable
		}
		return order.ErrInternal
	}
	var connect *pgconn.ConnectError
	var network net.Error
	if errors.As(err, &connect) || errors.As(err, &network) || pgconn.SafeToRetry(err) || strings.Contains(err.Error(), "closed") {
		return order.ErrUnavailable
	}
	return order.ErrInternal
}
