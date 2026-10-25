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
	"github.com/syuhei0519/backend-app/internal/account"
	"github.com/syuhei0519/backend-app/internal/runtimeprofile"
	"github.com/syuhei0519/backend-app/internal/schemahistory"
	"github.com/syuhei0519/backend-app/internal/telemetry"
	"time"
)

type Store struct {
	pool            *pgxpool.Pool
	observation     *telemetry.Telemetry
	profile         *runtimeprofile.Profile
	policy          schemahistory.Policy
	accountsTable   string
	migrationsTable string
	historyTable    string
}

var (
	errParseDatabaseConfiguration = errors.New("parse Account database configuration failed")
	errCreateDatabasePool         = errors.New("create Account database pool failed")
	errSchemaCompatibilityProfile = errors.New("invalid Account schema compatibility profile")
)

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
func Open(ctx context.Context, url string, profiles ...*runtimeprofile.Profile) (*Store, error) {
	if len(profiles) > 1 {
		return nil, errors.New("multiple Account runtime profiles")
	}
	var profile *runtimeprofile.Profile
	if len(profiles) == 1 {
		profile = profiles[0]
	}
	policy, err := schemahistory.LoadPolicy()
	if err != nil {
		return nil, errSchemaCompatibilityProfile
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, errParseDatabaseConfiguration
	}
	cfg.MinConns = 0
	cfg.MaxConns = 5
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.ConnConfig.ConnectTimeout = 3 * time.Second
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, errCreateDatabasePool
	}
	return &Store{
		pool:            pool,
		profile:         profile,
		policy:          policy,
		accountsTable:   profileRelation(profile, "accounts"),
		migrationsTable: profileRelation(profile, "schema_migrations"),
		historyTable:    profileHistoryTable(profile),
	}, nil
}

// profileRelation quotes the reviewed schema and relation as identifiers. The
// legacy no-profile path keeps the historical unqualified relation name.
func profileRelation(profile *runtimeprofile.Profile, relation string) string {
	if profile == nil || profile.SchemaName == "" {
		return relation
	}
	return pgx.Identifier{profile.SchemaName, relation}.Sanitize()
}

func profileHistoryTable(profile *runtimeprofile.Profile) string {
	if profile == nil || profile.SchemaName == "" {
		return "public.schema_migrations"
	}
	return profile.SchemaName + ".schema_migrations"
}

func (s *Store) accountRelation() string {
	if s.accountsTable != "" {
		return s.accountsTable
	}
	return profileRelation(s.profile, "accounts")
}

func (s *Store) migrationRelation() string {
	if s.migrationsTable != "" {
		return s.migrationsTable
	}
	return profileRelation(s.profile, "schema_migrations")
}

func (s *Store) migrationHistoryTable() string {
	if s.historyTable != "" {
		return s.historyTable
	}
	return profileHistoryTable(s.profile)
}

// 通常のプロセス終了でpoolを閉じる。借用中の接続の返却を待つ。
func (s *Store) Close() { s.pool.Close() }

// readyz用に1秒以内で履歴の最大versionが1か確認。DB接続成功だけでは互換schemaと判定しない。
func (s *Store) Ready(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if s.profile != nil {
		var database, role, owner, schemaOwner string
		err := s.pool.QueryRow(ctx, `SELECT current_database(),current_user,pg_get_userbyid(d.datdba),pg_get_userbyid(n.nspowner) FROM pg_database AS d JOIN pg_namespace AS n ON n.nspname=$1 WHERE d.datname=current_database()`, s.profile.SchemaName).Scan(&database, &role, &owner, &schemaOwner)
		if err != nil {
			return err
		}
		if err := validateRuntimeIdentity(s.profile, database, role, owner, schemaOwner); err != nil {
			return err
		}
	}
	if !s.policy.Ready(schemahistory.Inspect(ctx, s.pool, s.migrationHistoryTable())) {
		return account.ErrUnavailable
	}
	return nil
}

func validateRuntimeIdentity(profile *runtimeprofile.Profile, database, role, owner, schemaOwner string) error {
	if profile == nil || profile.Operation() != runtimeprofile.OperationRuntime {
		return errors.New("Account runtime profile rejected")
	}
	return profile.ValidateDatabaseIdentity(database, role, owner, schemaOwner)
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
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`SELECT id,name,email,created_at,updated_at FROM %s ORDER BY id`, s.accountRelation()))
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
	return scan(s.pool.QueryRow(ctx, fmt.Sprintf(`SELECT id,name,email,created_at,updated_at FROM %s WHERE id=$1`, s.accountRelation()), id))
}

// 単一INSERTはDB側で原子的に実行される。RETURNINGで採番・DB時刻を含む確定結果を返す。
func (s *Store) Create(ctx context.Context, input account.Input) (result account.Account, err error) {
	ctx, end := s.observe(ctx, "INSERT")
	defer func() { end(err) }()
	ctx, cancel := timeout(ctx)
	defer cancel()
	return scan(s.pool.QueryRow(ctx, fmt.Sprintf(`INSERT INTO %s(name,email) VALUES($1,$2) RETURNING id,name,email,created_at,updated_at`, s.accountRelation()), input.Name, input.Email))
}

// 更新値もパラメータ化。updated_atはDB時刻へ更新し、対象なしならErrNotFound。
func (s *Store) Update(ctx context.Context, id int64, input account.Input) (result account.Account, err error) {
	ctx, end := s.observe(ctx, "UPDATE")
	defer func() { end(err) }()
	ctx, cancel := timeout(ctx)
	defer cancel()
	return scan(s.pool.QueryRow(ctx, fmt.Sprintf(`UPDATE %s SET name=$2,email=$3,updated_at=now() WHERE id=$1 RETURNING id,name,email,created_at,updated_at`, s.accountRelation()), id, input.Name, input.Email))
}

// 削除件数0はErrNotFound。成功後HTTP層が204を返す。自動再試行は行わない。
func (s *Store) Delete(ctx context.Context, id int64) (err error) {
	ctx, end := s.observe(ctx, "DELETE")
	defer func() { end(err) }()
	ctx, cancel := timeout(ctx)
	defer cancel()
	tag, err := s.pool.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE id=$1`, s.accountRelation()), id)
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
