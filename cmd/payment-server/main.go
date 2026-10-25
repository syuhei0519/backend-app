// Payment サーバーの入口です。設定を読み、API 起動と DB migration の実行を切り替えます。
// payment-server is an independent Payment API or forward-only migration process.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/syuhei0519/backend-app/internal/payment"
	"github.com/syuhei0519/backend-app/internal/paymentconfig"
	"github.com/syuhei0519/backend-app/internal/paymenthttp"
	"github.com/syuhei0519/backend-app/internal/store/paymentpostgres"
)

// OCI build stamps preserve source identity for image scanning.
var buildCommit = "unknown"
var version = "0.0.0-dev"

func main() { os.Exit(run(os.Args[1:])) }
func run(args []string) int {
	if len(args) > 1 || (len(args) == 1 && args[0] != "migrate") {
		return 2
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := paymentconfig.Load()
	if err != nil {
		logger.Error("invalid Payment configuration")
		return 1
	}
	logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	logger.Info("Payment process starting", "build_version", version, "build_commit", buildCommit)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	store, err := paymentpostgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("Payment database configuration rejected")
		return 1
	}
	defer store.Close()
	if len(args) == 1 {
		if err = store.Migrate(ctx); err != nil {
			logger.Error("Payment migration failed")
			return 1
		}
		logger.Info("Payment migration completed")
		return 0
	}
	if err = store.CheckTarget(ctx); err != nil {
		logger.Error("Payment database target unavailable or rejected")
		return 1
	}
	handler := paymenthttp.New(payment.New(store), store)
	server := &http.Server{Addr: cfg.Address, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	stopped := make(chan error, 1)
	go func() { stopped <- server.ListenAndServe() }()
	select {
	case err = <-stopped:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("Payment server failed")
			return 1
		}
		return 0
	case <-ctx.Done():
		handler.BeginShutdown()
		time.Sleep(5 * time.Second)
	}
	cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err = server.Shutdown(cleanup); err != nil {
		_ = server.Close()
		logger.Error("Payment shutdown failed")
		return 1
	}
	return 0
}
