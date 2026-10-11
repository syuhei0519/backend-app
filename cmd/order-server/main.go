// order-server is an independent Order API or forward-only migration process.
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

	"gitlab.com/platform-engineering-lab/backend-app/internal/order"
	"gitlab.com/platform-engineering-lab/backend-app/internal/orderconfig"
	"gitlab.com/platform-engineering-lab/backend-app/internal/orderhttp"
	"gitlab.com/platform-engineering-lab/backend-app/internal/store/orderpostgres"
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
	cfg, err := orderconfig.Load()
	if err != nil {
		logger.Error("invalid Order configuration")
		return 1
	}
	logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	store, err := orderpostgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("Order database configuration rejected")
		return 1
	}
	defer store.Close()
	if len(args) == 1 {
		if err = store.Migrate(ctx); err != nil {
			logger.Error("Order migration failed")
			return 1
		}
		logger.Info("Order migration completed")
		return 0
	}
	if err = store.CheckTarget(ctx); err != nil {
		logger.Error("Order database target unavailable or rejected")
		return 1
	}
	handler := orderhttp.New(order.New(store), store)
	server := &http.Server{Addr: cfg.Address, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	stopped := make(chan error, 1)
	go func() { stopped <- server.ListenAndServe() }()
	select {
	case err = <-stopped:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("Order server failed")
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
		logger.Error("Order shutdown failed")
		return 1
	}
	return 0
}
