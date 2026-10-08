// バックエンド実行入口。コンテナの /server と migration Job の /server migrate が呼ぶ。
// 環境変数を internal/config/config.go で読み、HTTP JSON API または DB 移行を実行する。
// HTTP→internal/account/account.go→internal/store/postgres/store.go、観測→internal/telemetry/telemetry.go。
package main

import (
	"context"
	"errors"
	"gitlab.com/platform-engineering-lab/backend-app/internal/account"
	"gitlab.com/platform-engineering-lab/backend-app/internal/config"
	"gitlab.com/platform-engineering-lab/backend-app/internal/httpapi"
	"gitlab.com/platform-engineering-lab/backend-app/internal/store/postgres"
	"gitlab.com/platform-engineering-lab/backend-app/internal/telemetry"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

var buildCommit = "unknown"
var version = "development"

// JSONログの時刻キーを timestamp に統一する。収集側との契約は docs/telemetry-contract.md。
func newLogger(out io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(out, &slog.HandlerOptions{Level: level, ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
		if a.Key == slog.TimeKey {
			a.Key = "timestamp"
		}
		return a
	}}))
}

// 設定不正・起動失敗は終了コード1、不正な引数は2。通常 return は defer を逆順に実行する。
// os.Exit は defer を実行しないため、異常終了時の接続解放やtelemetry flushを保証するコードではない。
func main() {
	logger := newLogger(os.Stdout, slog.LevelInfo)
	cfg, err := config.Load()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	logger = newLogger(os.Stdout, cfg.LogLevel)
	logger.Debug("debug logging enabled")
	store, err := postgres.Open(context.Background(), cfg.DatabaseURL)
	if err != nil {
		logger.Error("initialize database pool", "error", err)
		os.Exit(1)
	}
	// 通常終了時にpoolを閉じる。Openはpool生成であり、DB接続成功・schema互換性の確認とは別。
	defer store.Close()
	// migrateはHTTPを起動しない。移行成功は0で終了し、ArgoがJob成功後にDeploymentへ進む。
	// 関連: application-manifest/charts/backend/templates/migration-job.yaml。
	if len(os.Args) == 2 && os.Args[1] == "migrate" {
		if err := store.Migrate(context.Background()); err != nil {
			logger.Error("migration failed", "error", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) != 1 {
		os.Exit(2)
	}
	logger.Info("server starting", "log_level", cfg.LogLevel.String(), "build_version", version)
	observation, err := telemetry.Init(context.Background(), buildCommit, logger)
	if err != nil {
		logger.Error("initialize telemetry failed")
		os.Exit(1)
	}
	defer func() {
		if observation.Shutdown() != nil {
			logger.Warn("telemetry shutdown incomplete")
		}
	}()
	store.SetTelemetry(observation)
	handler := httpapi.NewWithTelemetry(account.New(store), store, logger, observation)
	// ヘッダー読込5秒、応答書込10秒、idle60秒で接続の占有を制限する。数値の詳細な選定根拠は資料にない。
	// 前段Nginxの5秒timeoutとは別の上限で、利用者には先にNginx側の失敗が返る場合がある。
	server := &http.Server{Addr: cfg.Address, Handler: handler, ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	stopped := make(chan error, 1)
	go func() { stopped <- server.ListenAndServe() }()
	// contextは中断・期限を処理間で伝える器。SIGTERM/SIGINTを受けたらDoneが閉じる。
	// stopをdeferしてsignal登録も通常終了時に解放する。
	signals, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	select {
	case <-signals.Done():
		// 先にreadyzを503へ切り替え、Serviceから外れるまで5秒待つ。livezは成功のまま。
		// その後のdrain15秒と合わせ、DeploymentのterminationGracePeriodSeconds=30内に収める意図。
		handler.BeginShutdown()
		time.Sleep(5 * time.Second)
	case err := <-stopped:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server failed", "error", err)
			os.Exit(1)
		}
		return
	}
	// 新規の終了用contextに15秒の期限を設定。受信を止め、処理中HTTPの完了を待つ。
	// defer cancelでタイマーを解放する。期限超過はログ shutdown failed と終了コード1で確認する。
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		logger.Error("shutdown failed", "error", err)
		os.Exit(1)
	}
}
