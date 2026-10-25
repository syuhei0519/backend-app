// Order サーバーの起動引数が正しく判定され、不明な引数が拒否されることを確認します。
package main

import "testing"

func TestInvalidArguments(t *testing.T) {
	if run([]string{"unknown"}) != 2 || run([]string{"migrate", "extra"}) != 2 {
		t.Fatal("invalid args accepted")
	}
}
