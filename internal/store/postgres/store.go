// 業務StoreのPostgreSQL実装。internal/account/account.goからcontextと入力を受け、SQL結果をAccountへ変換する。
// DB spanはinternal/telemetry/telemetry.go、HTTPエラー変換はinternal/httpapi/server.goへつながる。
package postgres

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"gitlab.com/platform-engineering-lab/backend-app/internal/account"
	"gitlab.com/platform-engineering-lab/backend-app/internal/telemetry"
	"time"
)

type Store struct {
	pool        *pgxpool.Pool
	observation *telemetry.Telemetry
}

func (s *Store) SetTelemetry(t *telemetry.Telemetry) { s.observation = t }

// HTTPから受け取ったcontextをDB spanへ引き継ぐ。計装なしなら何もしない終了関数を返す。
func (s *Store) observe(ctx context.Context, operation string) (context.Context, func(error)) {
	if s.observation == nil {
		return ctx, func(error) {}
	}
	return s.observation.DB(ctx, operation)
}

// poolを構成する。MinConns=0なので起動時点でDBが利用可能とは保証しない。
// MaxConns=5、寿命30分、idle5分、接続3秒は現行上限。具体的な性能根拠は資料にない。
func Open(ctx context.Context, url string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database URL: %w", err)
	}
	cfg.MinConns = 0
	cfg.MaxConns = 5
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.ConnConfig.ConnectTimeout = 3 * time.Second
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	return &Store{pool: pool}, nil
}

// 通常のプロセス終了でpoolを閉じる。借用中の接続の返却を待つ。
func (s *Store) Close() { s.pool.Close() }

// readyz用に1秒以内で履歴の最大versionが1か確認。DB接続成功だけでは互換schemaと判定しない。
func (s *Store) Ready(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	var version int
	if err := s.pool.QueryRow(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	if version != 1 {
		return fmt.Errorf("incompatible schema version: %d", version)
	}
	return nil
}

// 各CRUD SQLを3秒に制限。親HTTPのキャンセルはこの子contextにも伝わる。
func timeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 3*time.Second)
}

// 一覧取得。rows.Closeは読み取り中断でも接続を解放し、rows.Errで反復途中の失敗を拾う。
// 名前付き戻り値errをdefer内で読み、最終結果でDB spanを終了する。
func (s *Store) List(ctx context.Context) (result []account.Account, err error) {
	ctx, end := s.observe(ctx, "SELECT")
	defer func() { end(err) }()
	ctx, cancel := timeout(ctx)
	defer cancel()
	rows, err := s.pool.Query(ctx, `SELECT id,name,email,created_at,updated_at FROM accounts ORDER BY id`)
	if err != nil {
		return nil, unavailable(err)
	}
	defer rows.Close()
	items := []account.Account{}
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, unavailableOrNil(rows.Err())
}

// IDをSQLの$1パラメータとして渡す。文字列連結せず、未検出はErrNotFoundへ変換する。
func (s *Store) Get(ctx context.Context, id int64) (result account.Account, err error) {
	ctx, end := s.observe(ctx, "SELECT")
	defer func() { end(err) }()
	ctx, cancel := timeout(ctx)
	defer cancel()
	return scan(s.pool.QueryRow(ctx, `SELECT id,name,email,created_at,updated_at FROM accounts WHERE id=$1`, id))
}

// 単一INSERTはDB側で原子的に実行される。RETURNINGで採番・DB時刻を含む確定結果を返す。
func (s *Store) Create(ctx context.Context, input account.Input) (result account.Account, err error) {
	ctx, end := s.observe(ctx, "INSERT")
	defer func() { end(err) }()
	ctx, cancel := timeout(ctx)
	defer cancel()
	return scan(s.pool.QueryRow(ctx, `INSERT INTO accounts(name,email) VALUES($1,$2) RETURNING id,name,email,created_at,updated_at`, input.Name, input.Email))
}

// 更新値もパラメータ化。updated_atはDB時刻へ更新し、対象なしならErrNotFound。
func (s *Store) Update(ctx context.Context, id int64, input account.Input) (result account.Account, err error) {
	ctx, end := s.observe(ctx, "UPDATE")
	defer func() { end(err) }()
	ctx, cancel := timeout(ctx)
	defer cancel()
	return scan(s.pool.QueryRow(ctx, `UPDATE accounts SET name=$2,email=$3,updated_at=now() WHERE id=$1 RETURNING id,name,email,created_at,updated_at`, id, input.Name, input.Email))
}

// 削除件数0はErrNotFound。成功後HTTP層が204を返す。自動再試行は行わない。
func (s *Store) Delete(ctx context.Context, id int64) (err error) {
	ctx, end := s.observe(ctx, "DELETE")
	defer func() { end(err) }()
	ctx, cancel := timeout(ctx)
	defer cancel()
	tag, err := s.pool.Exec(ctx, `DELETE FROM accounts WHERE id=$1`, id)
	if err != nil {
		return unavailable(err)
	}
	if tag.RowsAffected() == 0 {
		return account.ErrNotFound
	}
	return nil
}

type scanner interface{ Scan(...any) error }

// pgxの未検出とunique違反23505を業務エラーへ分類。その他は利用不能としてHTTP503へ渡す。
func scan(row scanner) (account.Account, error) {
	var item account.Account
	err := row.Scan(&item.ID, &item.Name, &item.Email, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return item, account.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return item, account.ErrConflict
	}
	if err != nil {
		return item, unavailable(err)
	}
	return item, nil
}
func unavailable(err error) error { return fmt.Errorf("%w: %v", account.ErrUnavailable, err) }
func unavailableOrNil(err error) error {
	if err == nil {
		return nil
	}
	return unavailable(err)
}
