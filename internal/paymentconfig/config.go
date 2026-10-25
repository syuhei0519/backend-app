// Payment サーバーの環境変数を読み、起動に必要な値と接続先の条件を確認します。
// Package paymentconfig holds independent Payment process settings.
package paymentconfig

import (
	"errors"
	"log/slog"
	"net"
	"os"
	"strings"
)

type Config struct {
	Address, DatabaseURL string
	LogLevel             slog.Level
}

func Load() (Config, error) {
	levels := map[string]slog.Level{"": slog.LevelInfo, "debug": slog.LevelDebug, "info": slog.LevelInfo, "warn": slog.LevelWarn, "error": slog.LevelError}
	level, ok := levels[strings.ToLower(os.Getenv("LOG_LEVEL"))]
	if !ok {
		return Config{}, errors.New("invalid LOG_LEVEL")
	}
	address := os.Getenv("PAYMENT_HTTP_ADDRESS")
	if address == "" {
		address = "0.0.0.0:8080"
	}
	if _, _, err := net.SplitHostPort(address); err != nil {
		return Config{}, errors.New("invalid PAYMENT_HTTP_ADDRESS")
	}
	return Config{Address: address, DatabaseURL: os.Getenv("DATABASE_URL"), LogLevel: level}, nil
}
