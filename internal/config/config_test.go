// LOG_LEVELの既定・許可値・不正値拒否を確認する単体試験。環境はt.Setenvでテスト後に復元する。
package config

import (
	"log/slog"
	"testing"
)

func TestLogLevelConfig(t *testing.T) {
	for _, test := range []struct {
		value string
		level slog.Level
	}{{"", slog.LevelInfo}, {"DEBUG", slog.LevelDebug}, {"info", slog.LevelInfo}, {"warn", slog.LevelWarn}, {"error", slog.LevelError}} {
		t.Run(test.value, func(t *testing.T) {
			t.Setenv("LOG_LEVEL", test.value)
			cfg, err := Load()
			if err != nil || cfg.LogLevel != test.level {
				t.Fatalf("level=%v err=%v", cfg.LogLevel, err)
			}
		})
	}
	t.Setenv("LOG_LEVEL", "invalid")
	if _, err := Load(); err == nil {
		t.Fatal("invalid configuration accepted")
	}
}
