// notification-server is an independent Notification API or forward-only migration process.
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

	"github.com/syuhei0519/backend-app/internal/notification"
	"github.com/syuhei0519/backend-app/internal/notificationconfig"
	"github.com/syuhei0519/backend-app/internal/notificationhttp"
	"github.com/syuhei0519/backend-app/internal/store/notificationpostgres"
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
	cfg, err := notificationconfig.Load()
	if err != nil {
		logger.Error("invalid Notification configuration")
		return 1
	}
	logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	logger.Info("Notification process starting", "build_version", version, "build_commit", buildCommit)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	store, err := notificationpostgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("Notification database configuration rejected")
		return 1
	}
	defer store.Close()
	if len(args) == 1 {
		if err = store.Migrate(ctx); err != nil {
			logger.Error("Notification migration failed")
			return 1
		}
		logger.Info("Notification migration completed")
		return 0
	}
	if err = store.CheckTarget(ctx); err != nil {
		logger.Error("Notification database target unavailable or rejected")
		return 1
	}
	handler := notificationhttp.New(notification.New(store), store)
	server := &http.Server{Addr: cfg.Address, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	stopped := make(chan error, 1)
	go func() { stopped <- server.ListenAndServe() }()
	select {
	case err = <-stopped:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("Notification server failed")
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
		logger.Error("Notification shutdown failed")
		return 1
	}
	return 0
}
