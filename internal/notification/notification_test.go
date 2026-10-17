package notification

import (
	"strings"
	"testing"
)

func validInput() Input {
	return Input{RequestID: "550e8400-e29b-41d4-a716-446655440001", OrderID: 1, Recipient: "demo-user", Message: "Order accepted", SimulateResult: "success"}
}
func TestValidationAndDecision(t *testing.T) {
	for _, change := range []func(*Input){
		func(i *Input) { i.RequestID = "00000000-0000-0000-0000-000000000000" }, func(i *Input) { i.RequestID = "550E8400-E29B-41D4-A716-446655440001" }, func(i *Input) { i.RequestID = "invalid" }, func(i *Input) { i.OrderID = 0 }, func(i *Input) { i.Recipient = "_user" }, func(i *Input) { i.Recipient = strings.Repeat("a", 65) }, func(i *Input) { i.Message = "a\x00b" }, func(i *Input) { i.Message = "" }, func(i *Input) { i.Message = strings.Repeat("a", 1025) }, func(i *Input) { i.Message = string([]byte{255}) }, func(i *Input) { i.SimulateResult = "decline" },
	} {
		in := validInput()
		change(&in)
		if Validate(in) != ErrInvalid {
			t.Fatal("invalid input accepted")
		}
	}
	in := validInput()
	in.OrderID = 9223372036854775807
	in.Recipient = strings.Repeat("a", 64)
	in.Message = strings.Repeat("é", 512)
	if Validate(in) != nil || Result(in) != "sent" {
		t.Fatal("valid boundary")
	}
	in.SimulateResult = "failure"
	if Validate(in) != nil || Result(in) != "failed" {
		t.Fatal("fake failure")
	}
}
