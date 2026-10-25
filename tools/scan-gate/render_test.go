// 描画結果の検査が、許可する構成と禁止する構成を正しく区別することを確認します。
package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderedPlainSecretAndPrivileged(t *testing.T) {
	for _, data := range []string{"kind: Secret\nstringData:\n  password: DO_NOT_EXPORT_672341\n", "kind: Secret\ndata:\n  key: ZmFrZQ==\n", "kind: Pod\nspec:\n  containers:\n  - name: demo\n    env:\n    - name: API_TOKEN\n      value: DO_NOT_EXPORT_672341\n", "kind: Pod\nspec:\n  containers:\n  - name: demo\n    securityContext:\n      privileged: true\n"} {
		f, e := inspectYAML([]byte(data), "rendered/test.yaml")
		b, _ := json.Marshal(f)
		if e != nil || len(f) == 0 || strings.Contains(string(b), "DO_NOT_EXPORT_672341") {
			t.Fatal("render hard gate or redaction failed")
		}
	}
	allowed := "kind: Pod\nspec:\n  containers:\n  - name: demo\n    env:\n    - name: API_TOKEN\n      valueFrom:\n        secretKeyRef: {name: operator-owned, key: token}\n    securityContext:\n      privileged: false\n"
	if f, e := inspectYAML([]byte(allowed), "rendered/test.yaml"); e != nil || len(f) != 0 {
		t.Fatal("SecretKeyRef/nonprivileged boundary rejected")
	}
	if _, e := inspectYAML([]byte("kind: Pod\nkind: Secret\n"), "rendered/test.yaml"); e == nil {
		t.Fatal("duplicate render key accepted")
	}
}

func TestRenderedCredentialURLAndConfigMap(t *testing.T) {
	const sentinel = "DO_NOT_EXPORT_672341"
	for _, data := range []string{
		"kind: Pod\nspec:\n  containers:\n  - name: demo\n    env:\n    - name: DATABASE_URL\n      value: postgres://operator:" + sentinel + "@db/app\n",
		"kind: ConfigMap\ndata:\n  API_TOKEN: " + sentinel + "\n",
	} {
		f, err := inspectYAML([]byte(data), "rendered/test.yaml")
		b, _ := json.Marshal(f)
		if err != nil || len(f) != 1 || f[0].Rule != "LOCAL-PLAINTEXT-SECRET" || strings.Contains(string(b), sentinel) {
			t.Fatal("credential URL/ConfigMap hard gate or redaction failed")
		}
	}
	allowed := "kind: ConfigMap\ndata:\n  API_ENDPOINT: https://api.example.invalid/v1\n  LOG_LEVEL: info\n"
	if f, err := inspectYAML([]byte(allowed), "rendered/test.yaml"); err != nil || len(f) != 0 {
		t.Fatal("noncredential configuration rejected")
	}
}

func TestRenderedEmptySecretValues(t *testing.T) {
	for _, data := range []string{
		"kind: Secret\ndata: {}\nstringData: {}\n",
		"kind: Secret\ndata:\n  empty: \"\"\nstringData:\n  PLUGINS-HTTP-PROXY: \"\"\n  PLUGINS-HTTPS-PROXY: \"\"\n",
	} {
		if findings, err := inspectYAML([]byte(data), "rendered/test.yaml"); err != nil || len(findings) != 0 {
			t.Fatal("empty configuration must not be treated as a credential")
		}
	}
	for _, data := range []string{
		"kind: Secret\nstringData:\n  empty: \"\"\n  password: DO_NOT_EXPORT_672341\n",
		"kind: Secret\ndata:\n  empty: \"\"\n  password: ZmFrZQ==\n",
		"kind: Secret\nstringData:\n  password: ' '\n",
		"kind: Secret\nstringData:\n  password: null\n",
		"kind: Secret\nstringData:\n  password: false\n",
		"kind: Secret\nstringData:\n  password: {}\n",
		"kind: Secret\nstringData: []\n",
		"kind: Secret\ndata: null\n",
		"kind: Secret\nstringData:\n  42: \"\"\n",
	} {
		findings, err := inspectYAML([]byte(data), "rendered/test.yaml")
		b, _ := json.Marshal(findings)
		if err != nil || len(findings) != 1 || findings[0].Rule != "LOCAL-PLAINTEXT-SECRET" || strings.Contains(string(b), "DO_NOT_EXPORT_672341") {
			t.Fatal("nonempty or malformed Secret bypassed the redacted hard gate")
		}
	}
}

func TestOnlyReadOnlySecretVolumeFileReferenceAllowed(t *testing.T) {
	valid := "kind: Pod\nspec:\n  containers:\n  - name: db\n    env:\n    - name: POSTGRES_PASSWORD_FILE\n      value: /run/account-db/admin-password\n    volumeMounts:\n    - name: account-db\n      mountPath: /run/account-db\n      readOnly: true\n  volumes:\n  - name: account-db\n    secret: {secretName: operator-owned}\n"
	if f, err := inspectYAML([]byte(valid), "rendered/db.yaml"); err != nil || len(f) != 0 {
		t.Fatal("read-only Secret file reference rejected")
	}
	for _, invalid := range []string{
		strings.Replace(valid, "readOnly: true", "readOnly: false", 1),
		strings.Replace(valid, "secret: {secretName: operator-owned}", "emptyDir: {}", 1),
		strings.Replace(valid, "value: /run/account-db/admin-password", "value: /tmp/admin-password", 1),
		strings.Replace(valid, "value: /run/account-db/admin-password", "value: /run/account-db/../other/password", 1),
		strings.Replace(valid, "value: /run/account-db/admin-password", "value: DO_NOT_EXPORT_672341", 1),
		strings.Replace(valid, "POSTGRES_PASSWORD_FILE", "POSTGRES_PASSWORD", 1),
		strings.Replace(valid, "mountPath: /run/account-db", "mountPath: /", 1),
		strings.Replace(valid, "readOnly: true", "readOnly: true\n      subPath: admin-password", 1),
	} {
		f, err := inspectYAML([]byte(invalid), "rendered/db.yaml")
		if err != nil || len(f) != 1 || f[0].Rule != "LOCAL-PLAINTEXT-SECRET" {
			t.Fatal("unproven Secret file or literal credential allowed")
		}
	}
}
