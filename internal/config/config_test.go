// LOG_LEVELの既定・許可値・不正値拒否を確認する単体試験。環境はt.Setenvでテスト後に復元する。
package config

import (
	"github.com/syuhei0519/backend-app/internal/runtimeprofile"
	"log/slog"
	"os"
	"strings"
	"testing"
)

func clearRuntimeProfile(t *testing.T) {
	t.Helper()
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key == "DATABASE_URL" || key == "ACCOUNT_RUNTIME_PROFILE" || strings.HasPrefix(key, "ACCOUNT_RUNTIME_PROFILE_") || strings.HasPrefix(key, "PG") || strings.HasPrefix(key, "OTEL_EXPORTER_OTLP_") || key == "POD_NAMESPACE" || key == "POD_UID" || key == "DEPLOYMENT_ENVIRONMENT" {
			t.Setenv(key, "")
		}
	}
}

func TestLogLevelConfig(t *testing.T) {
	clearRuntimeProfile(t)
	for _, test := range []struct {
		value string
		level slog.Level
	}{{"", slog.LevelInfo}, {"DEBUG", slog.LevelDebug}, {"info", slog.LevelInfo}, {"warn", slog.LevelWarn}, {"error", slog.LevelError}} {
		t.Run(test.value, func(t *testing.T) {
			t.Setenv("LOG_LEVEL", test.value)
			cfg, err := Load(runtimeprofile.OperationRuntime)
			if err != nil || cfg.LogLevel != test.level {
				t.Fatalf("level=%v err=%v", cfg.LogLevel, err)
			}
		})
	}
	t.Setenv("LOG_LEVEL", "invalid")
	if _, err := Load(runtimeprofile.OperationRuntime); err == nil {
		t.Fatal("invalid configuration accepted")
	}
}

func TestUnknownRuntimeProfileRejected(t *testing.T) {
	clearRuntimeProfile(t)
	t.Setenv("ACCOUNT_RUNTIME_PROFILE", "unknown")
	if _, err := Load(runtimeprofile.OperationRuntime); err == nil {
		t.Fatal("unknown runtime profile accepted")
	}
}

func TestAccountAddressIsFinite(t *testing.T) {
	clearRuntimeProfile(t)
	t.Setenv("ACCOUNT_HTTP_ADDRESS", "127.0.0.1:18081")
	got, err := Load(runtimeprofile.OperationRuntime)
	if err != nil || got.Address != "127.0.0.1:18081" {
		t.Fatalf("address=%q err=%v", got.Address, err)
	}
	t.Setenv("ACCOUNT_HTTP_ADDRESS", "not-an-address")
	if _, err := Load(runtimeprofile.OperationRuntime); err == nil {
		t.Fatal("invalid Account address accepted")
	}
}
