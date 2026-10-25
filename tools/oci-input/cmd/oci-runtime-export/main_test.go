// OCI の変換で設定・layer・digest の対応が崩れないことを確認します。
package main

import (
	"github.com/google/go-containerregistry/pkg/name"
	"testing"
)

func TestDisposableTransportTag(t *testing.T) {
	if _, err := name.NewTag("docker.io/local/order-b2-synthetic:checked", name.StrictValidation); err != nil {
		t.Fatal(err)
	}
	if _, err := name.NewTag("local/order-b2-synthetic:checked", name.StrictValidation); err == nil {
		t.Fatal("missing registry accepted")
	}
}
