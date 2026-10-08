// 起動設定を環境から読む。cmd/server/main.go が呼び、DB接続指定・待受先・ログレベルのConfigまたは検証エラーを返す。
// DB資格情報の注入元は application-manifest/charts/backend/templates/_helpers.tpl。
package config

import (
	"errors"
	"log/slog"
	"os"
	"strings"
)

type Config struct {
	Address     string
	DatabaseURL string
	LogLevel    slog.Level
}

// LOG_LEVELの許可値を検証し、不正値なら起動を止める。DB URLの実値はログへ出さない。
func Load() (Config, error) {
	// DATABASE_URLが空ならpgxがPGHOST/PGPORT/PGDATABASE/PGUSER/PGPASSWORD/PGSSLMODEを読む。
	// Secretのキーを個別の環境変数へ注入でき、manifest内で資格情報入りURLを組み立てずに済む。
	levels := map[string]slog.Level{"": slog.LevelInfo, "debug": slog.LevelDebug, "info": slog.LevelInfo, "warn": slog.LevelWarn, "error": slog.LevelError}
	level, ok := levels[strings.ToLower(os.Getenv("LOG_LEVEL"))]
	if !ok {
		return Config{}, errors.New("LOG_LEVEL must be debug, info, warn or error")
	}
	return Config{Address: "0.0.0.0:8080", DatabaseURL: os.Getenv("DATABASE_URL"), LogLevel: level}, nil
}
