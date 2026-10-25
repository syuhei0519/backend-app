// Order サーバーの環境変数を読み、起動に必要な値と接続先の条件を確認します。
// Package orderconfig holds independent Order process settings.
package orderconfig

import (
	"errors"
	"log/slog"
	"net"
	"os"
	"strings"

	"github.com/syuhei0519/backend-app/internal/accountclient"
	"github.com/syuhei0519/backend-app/internal/paymentclient"
)

type Config struct {
	Address, DatabaseURL string
	PaymentURL           string
	AccountURL           string
	LogLevel             slog.Level
}

func Load() (Config, error) {
	levels := map[string]slog.Level{"": slog.LevelInfo, "debug": slog.LevelDebug, "info": slog.LevelInfo, "warn": slog.LevelWarn, "error": slog.LevelError}
	level, ok := levels[strings.ToLower(os.Getenv("LOG_LEVEL"))]
	if !ok {
		return Config{}, errors.New("invalid LOG_LEVEL")
	}
	address := os.Getenv("ORDER_HTTP_ADDRESS")
	if address == "" {
		address = "0.0.0.0:8080"
	}
	if _, _, err := net.SplitHostPort(address); err != nil {
		return Config{}, errors.New("invalid ORDER_HTTP_ADDRESS")
	}
	accountURL := os.Getenv("ORDER_ACCOUNT_URL")
	if accountURL != "" {
		if err := accountclient.ValidateOrigin(accountURL); err != nil {
			return Config{}, err
		}
	}
	paymentURL := os.Getenv("ORDER_PAYMENT_URL")
	if paymentURL != "" {
		if err := paymentclient.ValidateOrigin(paymentURL); err != nil {
			return Config{}, err
		}
	}
	return Config{Address: address, DatabaseURL: os.Getenv("DATABASE_URL"), PaymentURL: paymentURL, AccountURL: accountURL, LogLevel: level}, nil
}
