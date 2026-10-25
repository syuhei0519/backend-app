// Payment の環境変数について、省略時の値と不正な設定の拒否を確認します。
package paymentconfig

import "testing"

func TestLoad(t *testing.T) {
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("PAYMENT_HTTP_ADDRESS", "")
	t.Setenv("DATABASE_URL", "")
	c, e := Load()
	if e != nil || c.Address != "0.0.0.0:8080" {
		t.Fatal("default")
	}
	t.Setenv("PAYMENT_HTTP_ADDRESS", "127.0.0.1:18080")
	if _, e = Load(); e != nil {
		t.Fatal("address")
	}
	t.Setenv("LOG_LEVEL", "unsafe")
	if _, e = Load(); e == nil {
		t.Fatal("level accepted")
	}
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("PAYMENT_HTTP_ADDRESS", "invalid")
	if _, e = Load(); e == nil {
		t.Fatal("address accepted")
	}
}
