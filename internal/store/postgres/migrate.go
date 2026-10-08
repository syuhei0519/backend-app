// /server migrateから呼ばれるDB移行。migrations/embed.goのSQLを順に適用し、履歴をDBへ保存する。
// Job契約は application-manifest/charts/backend/templates/migration-job.yaml、運用説明はdocs/migration-contract.md。
package postgres

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"gitlab.com/platform-engineering-lab/backend-app/migrations"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 同じDB内で協調する移行プロセスが共通に使うロック番号。SQLを直接実行する第三者までは排除しない。
const lockID int64 = 7742340081

// 待機・ロック・SQL適用を同じ親期限の下で行い、最初の失敗を返す。
// 各SQLファイル単位でcommitするため、後続ファイル失敗でも前のファイルの成功は残る。
func (s *Store) Migrate(parent context.Context) error {
	// Jobの180秒期限に対して処理は170秒までとし、後始末・プロセス終了用に10秒残す。
	ctx, cancel := context.WithTimeout(parent, 170*time.Second)
	defer cancel()
	if err := waitForDatabase(ctx, s.pool.Ping, waitRetry); err != nil {
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
	if _, err = conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, lockID); err != nil {
		return err
	}
	locked = true
	if _, err = conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations(version INTEGER PRIMARY KEY,applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return err
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
		if err = conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, version).Scan(&applied); err != nil {
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
		if err = applyMigration(ctx, conn, version, string(script)); err != nil {
			return err
		}
	}
	return nil
}

// transactionはSQL変更と履歴INSERTをまとめて確定する単位。同じ接続で開始し両方成功した時だけcommit。
// エラーや中断は独立5秒contextでrollbackを試みる。commit後のRollbackは無効だが戻り値を捨てている。
func applyMigration(ctx context.Context, conn *pgxpool.Conn, version int, script string) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	if _, err = tx.Exec(ctx, script); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES($1)`, version); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
