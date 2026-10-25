// Order の環境変数について、省略時の値と不正な設定の拒否を確認します。
package orderconfig

import "testing"

func TestLoad(t *testing.T) {
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("ORDER_HTTP_ADDRESS", "")
	t.Setenv("DATABASE_URL", "")
	c, e := Load()
	if e != nil || c.Address != "0.0.0.0:8080" {
		t.Fatal("default")
	}
	t.Setenv("ORDER_HTTP_ADDRESS", "127.0.0.1:18080")
	if _, e = Load(); e != nil {
		t.Fatal("address")
	}
	t.Setenv("LOG_LEVEL", "unsafe")
	if _, e = Load(); e == nil {
		t.Fatal("level accepted")
	}
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("ORDER_HTTP_ADDRESS", "invalid")
	if _, e = Load(); e == nil {
		t.Fatal("address accepted")
	}
}

func TestPaymentURL(t *testing.T) {
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("ORDER_HTTP_ADDRESS", "")
	for _, value := range []string{"", "http://learning-payment:8080"} {
		t.Setenv("ORDER_PAYMENT_URL", value)
		c, err := Load()
		if err != nil || c.PaymentURL != value {
			t.Fatal("payment URL")
		}
	}
	t.Setenv("ORDER_PAYMENT_URL", "http://private:password@host:8080/api")
	if _, err := Load(); err == nil || err.Error() != "invalid ORDER_PAYMENT_URL" {
		t.Fatal("unsafe config error")
	}
}

func TestAccountURL(t *testing.T) {
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("ORDER_HTTP_ADDRESS", "")
	t.Setenv("ORDER_PAYMENT_URL", "")
	for _, value := range []string{"", "http://learning-account:8080", "http://127.0.0.1:8080", "http://[::1]:8080"} {
		t.Setenv("ORDER_ACCOUNT_URL", value)
		c, err := Load()
		if err != nil || c.AccountURL != value {
			t.Fatal("account origin")
		}
	}
	for _, value := range []string{"https://host:8080", "http://host", "http://host:0", "http://host:65536", "http://host:8080/", "http://user:password@host:8080", "http://host:8080?secret=value", "http://host:8080#", "http://bad_host:8080"} {
		t.Setenv("ORDER_ACCOUNT_URL", value)
		if _, err := Load(); err == nil || err.Error() != "invalid ORDER_ACCOUNT_URL" {
			t.Fatal("invalid origin accepted or echoed")
		}
	}
}
