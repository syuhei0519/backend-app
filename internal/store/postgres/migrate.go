// /server migrateから呼ばれるDB移行。migrations/embed.goのSQLを順に適用し、履歴をDBへ保存する。
// Job契約は application-manifest/charts/backend/templates/migration-job.yaml、運用説明はdocs/migration-contract.md。
package postgres

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/syuhei0519/backend-app/internal/account"
	"github.com/syuhei0519/backend-app/internal/runtimeprofile"
	"github.com/syuhei0519/backend-app/internal/schemahistory"
	"github.com/syuhei0519/backend-app/internal/store/dbwait"
	"github.com/syuhei0519/backend-app/migrations"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 同じDB内で協調する移行プロセスが共通に使うロック番号。SQLを直接実行する第三者までは排除しない。
const lockID int64 = 7742340081

type migrationPreparationConn interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type migrationStatements struct {
	create, exists, insert, searchPath string
}

var (
	legacyMigrationSQL = migrationStatements{
		create: `CREATE TABLE IF NOT EXISTS schema_migrations(version INTEGER PRIMARY KEY,applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		exists: `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`,
		insert: `INSERT INTO schema_migrations(version) VALUES($1)`,
	}
	publicMigrationSQL = migrationStatements{
		create:     `CREATE TABLE IF NOT EXISTS public.schema_migrations(version INTEGER PRIMARY KEY,applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		exists:     `SELECT EXISTS(SELECT 1 FROM public.schema_migrations WHERE version=$1)`,
		insert:     `INSERT INTO public.schema_migrations(version) VALUES($1)`,
		searchPath: `SET LOCAL search_path TO public, pg_catalog`,
	}
	freshMigrationSQL = migrationStatements{
		create:     `CREATE TABLE IF NOT EXISTS account.schema_migrations(version INTEGER PRIMARY KEY,applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		exists:     `SELECT EXISTS(SELECT 1 FROM account.schema_migrations WHERE version=$1)`,
		insert:     `INSERT INTO account.schema_migrations(version) VALUES($1)`,
		searchPath: `SET LOCAL search_path TO account, pg_catalog`,
	}
)

func migrationStatementsFor(profile *runtimeprofile.Profile) migrationStatements {
	if profile == nil {
		return legacyMigrationSQL
	}
	switch profile.SchemaName {
	case "public":
		return publicMigrationSQL
	case "account":
		return freshMigrationSQL
	default:
		return migrationStatements{}
	}
}

// 最初のadvisory lock・DDLより前に実DB identityを照合する。この読取に履歴tableは不要。
func prepareMigration(ctx context.Context, conn migrationPreparationConn, profile *runtimeprofile.Profile) (bool, error) {
	if profile != nil {
		if profile.Operation() != runtimeprofile.OperationMigrate {
			return false, errors.New("Account migration profile rejected")
		}
		var database, role, owner, schemaOwner string
		err := conn.QueryRow(ctx, `SELECT current_database(),current_user,pg_get_userbyid(d.datdba),pg_get_userbyid(n.nspowner) FROM pg_database AS d JOIN pg_namespace AS n ON n.nspname=$1 WHERE d.datname=current_database()`, profile.SchemaName).Scan(&database, &role, &owner, &schemaOwner)
		if err != nil {
			return false, err
		}
		if err := profile.ValidateDatabaseIdentity(database, role, owner, schemaOwner); err != nil {
			return false, err
		}
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, lockID); err != nil {
		return false, err
	}
	statements := migrationStatementsFor(profile)
	if statements.create == "" {
		return false, errors.New("Account migration schema rejected")
	}
	if _, err := conn.Exec(ctx, statements.create); err != nil {
		return true, err
	}
	return true, nil
}

// 待機・ロック・SQL適用を同じ親期限の下で行い、最初の失敗を返す。
// 各SQLファイル単位でcommitするため、後続ファイル失敗でも前のファイルの成功は残る。
func (s *Store) Migrate(parent context.Context) error {
	// Jobの180秒期限に対して処理は170秒までとし、後始末・プロセス終了用に10秒残す。
	ctx, cancel := context.WithTimeout(parent, 170*time.Second)
	defer cancel()
	if err := dbwait.Wait(ctx, s.pool.Ping); err != nil {
		return err
	}
	// session advisory lockは接続単位。poolから借りた同じ接続でlock・履歴照合・transaction・unlockを行う。
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	// deferはこの関数のreturn時に実行する後始末。期限切れのctxを使い回さず、新しい5秒contextでunlockを試みる。
	locked := false
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		var unlocked bool
		if locked && conn.QueryRow(cleanup, `SELECT pg_advisory_unlock($1)`, lockID).Scan(&unlocked) == nil && unlocked {
			conn.Release()
			return
		}
		// lock中断やunlock失敗ではsessionの状態が確定しない。
		// ロックが残り得る接続はpoolへ返さず、Hijackして閉じる。
		_ = conn.Hijack().Close(cleanup)
	}()
	// 他の移行がロックを持つ間は待機するが、170秒の全体期限で打ち切る。
	// ロック後に履歴を見ることで「未適用と判断した2プロセスが同時適用する」競合を防ぐ。
	if locked, err = prepareMigration(ctx, conn, s.profile); err != nil {
		return err
	}
	historyTable := s.migrationHistoryTable()
	statements := migrationStatementsFor(s.profile)
	if statements.exists == "" {
		return account.ErrUnavailable
	}
	state := schemahistory.Inspect(ctx, conn, historyTable)
	if !schemahistory.C0MigrationAllowed(state) {
		return account.ErrUnavailable
	}
	if state == schemahistory.C0 {
		return nil
	}
	entries, err := fs.ReadDir(migrations.Files, ".")
	if err != nil {
		return err
	}
	// ファイル名の辞書順で適用する。0001_のようなゼロ埋め番号を運用上の前提とする。
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version, err := strconv.Atoi(strings.SplitN(entry.Name(), "_", 2)[0])
		if err != nil {
			return err
		}
		var applied bool
		if err = conn.QueryRow(ctx, statements.exists, version).Scan(&applied); err != nil {
			return err
		}
		// schema_migrationsにあるversionは再実行しない。履歴にはSQL checksumを保存しないので既存SQLの改変検出とは別。
		if applied {
			continue
		}
		script, err := migrations.Files.ReadFile(entry.Name())
		if err != nil {
			return err
		}
		if err = applyMigration(ctx, conn, s.profile, version, string(script)); err != nil {
			return err
		}
	}
	if !schemahistory.C0Ready(schemahistory.Inspect(ctx, conn, historyTable)) {
		return account.ErrUnavailable
	}
	return nil
}

// transactionはSQL変更と履歴INSERTをまとめて確定する単位。同じ接続で開始し両方成功した時だけcommit。
// エラーや中断は独立5秒contextでrollbackを試みる。commit後のRollbackは無効だが戻り値を捨てている。
func applyMigration(ctx context.Context, conn *pgxpool.Conn, profile *runtimeprofile.Profile, version int, script string) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	statements := migrationStatementsFor(profile)
	if statements.insert == "" {
		return errors.New("Account migration schema rejected")
	}
	if statements.searchPath != "" {
		if _, err = tx.Exec(ctx, statements.searchPath); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, script); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, statements.insert, version); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
