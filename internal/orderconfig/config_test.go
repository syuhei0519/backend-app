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
