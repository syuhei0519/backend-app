// JSONログのlevel切替とtimestampキーの回帰試験。newLoggerへメモリwriterを注入して確認する。
// 外部ログ基盤へ送らず、DEBUGが既定で漏れないことを検証する。
package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"strings"
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

func TestExplicitProfileDriverOverrideDoesNotLeakFromMain(t *testing.T) {
	const marker = "PRIVATE_PGMINPROTOCOLVERSION_CANARY"
	if os.Getenv("ACCOUNT_PROFILE_MAIN_HELPER") == "1" {
		os.Args = []string{os.Args[0]}
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestExplicitProfileDriverOverrideDoesNotLeakFromMain$")
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"SYSTEMROOT=" + os.Getenv("SYSTEMROOT"),
		"ACCOUNT_PROFILE_MAIN_HELPER=1",
		"ACCOUNT_RUNTIME_PROFILE=current-existing-kind-account",
		"PGHOST=learning-account-db",
		"PGPORT=5432",
		"PGDATABASE=account",
		"PGUSER=account_app",
		"PGPASSWORD=fixture-secret",
		"PGSSLMODE=disable",
		"POD_NAMESPACE=learning-services-20261018",
		"POD_UID=fixture-pod-uid",
		"DEPLOYMENT_ENVIRONMENT=local",
		"OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector.observability.svc.cluster.local:4318",
		"PGMINPROTOCOLVERSION=" + marker,
	}
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("unsafe PostgreSQL override accepted")
	}
	text := string(output)
	if strings.Contains(text, marker) {
		t.Fatal("PostgreSQL override leaked from main log")
	}
	if !strings.Contains(text, "invalid configuration") || !strings.Contains(text, "invalid Account runtime profile") {
		t.Fatal("main did not emit the fixed safe configuration error")
	}
}

func TestOperationFromArgs(t *testing.T) {
	for _, test := range []struct {
		args      []string
		operation string
		ok        bool
	}{
		{[]string{"server"}, "runtime", true},
		{[]string{"server", "migrate"}, "migrate", true},
		{[]string{"server", "other"}, "", false},
		{[]string{"server", "migrate", "extra"}, "", false},
	} {
		operation, ok := operationFromArgs(test.args)
		if ok != test.ok || string(operation) != test.operation {
			t.Fatalf("args=%q operation=%q ok=%v", test.args, operation, ok)
		}
	}
}

func TestFreshOperationRoleDoesNotLeakFromMain(t *testing.T) {
	const marker = "PRIVATE_FRESH_ROLE_CANARY"
	if operation := os.Getenv("ACCOUNT_FRESH_MAIN_HELPER"); operation != "" {
		os.Args = []string{os.Args[0]}
		if operation == "migrate" {
			os.Args = append(os.Args, "migrate")
		}
		main()
		return
	}
	for _, operation := range []string{"runtime", "migrate"} {
		t.Run(operation, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestFreshOperationRoleDoesNotLeakFromMain$/"+operation+"$")
			cmd.Env = []string{
				"PATH=" + os.Getenv("PATH"),
				"SYSTEMROOT=" + os.Getenv("SYSTEMROOT"),
				"ACCOUNT_FRESH_MAIN_HELPER=" + operation,
				"ACCOUNT_RUNTIME_PROFILE=fresh-account",
				"PGHOST=account-postgresql.fresh-account.svc",
				"PGPORT=5432",
				"PGDATABASE=account",
				"PGUSER=" + marker,
				"PGPASSWORD=fixture-secret",
				"PGSSLMODE=disable",
				"POD_NAMESPACE=fresh-account",
				"POD_UID=fixture-pod-uid",
				"DEPLOYMENT_ENVIRONMENT=local",
				"OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector.fresh-observability.svc.cluster.local:4318",
			}
			output, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatal("unsafe fresh role accepted")
			}
			text := string(output)
			if strings.Contains(text, marker) {
				t.Fatal("fresh role leaked from main log")
			}
			if !strings.Contains(text, "invalid configuration") || !strings.Contains(text, "invalid Account runtime profile") {
				t.Fatal("main did not emit the fixed safe configuration error")
			}
		})
	}
}
