package payment

import (
	"math"
	"strings"
	"testing"
)

func validInput() Input {
	return Input{"550e8400-e29b-41d4-a716-446655440000", 1, 1000, "JPY", "success"}
}
func TestValidationAndResult(t *testing.T) {
	good := validInput()
	for _, result := range []string{"success", "decline"} {
		in := good
		in.SimulateResult = result
		in.OrderID = math.MaxInt64
		in.AmountMinor = math.MaxInt64
		if Validate(in) != nil {
			t.Fatal("positive int64 boundary")
		}
		want := "succeeded"
		if result == "decline" {
			want = "declined"
		}
		if Result(in) != want {
			t.Fatal("fake result")
		}
	}
	bad := []Input{}
	for _, id := range []string{"", "00000000-0000-0000-0000-000000000000", strings.ToUpper(good.RequestID), "{" + good.RequestID + "}", strings.ReplaceAll(good.RequestID, "-", "")} {
		in := good
		in.RequestID = id
		bad = append(bad, in)
	}
	for _, n := range []int64{0, -1} {
		in := good
		in.OrderID = n
		bad = append(bad, in)
		in = good
		in.AmountMinor = n
		bad = append(bad, in)
	}
	in := good
	in.Currency = "USD"
	bad = append(bad, in)
	in = good
	in.SimulateResult = "failure"
	bad = append(bad, in)
	for _, in := range bad {
		if Validate(in) != ErrInvalid {
			t.Fatal("invalid input accepted")
		}
	}
}
