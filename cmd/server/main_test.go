// JSONログのlevel切替とtimestampキーの回帰試験。newLoggerへメモリwriterを注入して確認する。
// 外部ログ基盤へ送らず、DEBUGが既定で漏れないことを検証する。
package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestRuntimeLogLevelAndTimestamp(t *testing.T) {
	var out bytes.Buffer
	logger := newLogger(&out, slog.LevelInfo)
	logger.Debug("must be filtered")
	if out.Len() != 0 {
		t.Fatal("default emitted debug")
	}
	logger = newLogger(&out, slog.LevelDebug)
	logger.Debug("debug enabled")
	var record map[string]any
	if err := json.Unmarshal(out.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if record["level"] != "DEBUG" || record["msg"] != "debug enabled" || record["timestamp"] == nil || record["time"] != nil {
		t.Fatal("runtime logging contract changed")
	}
}
