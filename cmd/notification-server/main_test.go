package main

import "testing"

func TestInvalidArguments(t *testing.T) {
	if run([]string{"unknown"}) != 2 || run([]string{"migrate", "extra"}) != 2 {
		t.Fatal("invalid args accepted")
	}
}
