package order

import (
	"context"
	"math"
	"strings"
	"testing"
)

type fake struct{ calls int }

func (s *fake) Create(_ context.Context, in Input) (Order, error) {
	s.calls++
	return Order{AccountID: in.AccountID}, nil
}
func (s *fake) Get(_ context.Context, id int64) (Order, error) { s.calls++; return Order{ID: id}, nil }
func TestValidation(t *testing.T) {
	for _, in := range []Input{{1, "A", 1}, {math.MaxInt64, strings.Repeat("x", 64), 999}, {2, "a._-0", 2}} {
		f := &fake{}
		if _, err := New(f).Create(context.Background(), in); err != nil || f.calls != 1 {
			t.Fatal("valid input rejected")
		}
	}
	for _, in := range []Input{{0, "A", 1}, {-1, "A", 1}, {1, "", 1}, {1, " A", 1}, {1, "A ", 1}, {1, "é", 1}, {1, "_A", 1}, {1, "a\n", 1}, {1, strings.Repeat("a", 65), 1}, {1, "A", 0}, {1, "A", 1000}} {
		f := &fake{}
		if _, err := New(f).Create(context.Background(), in); err != ErrInvalid || f.calls != 0 {
			t.Fatal("invalid input reached store")
		}
	}
	f := &fake{}
	for _, id := range []int64{0, -1} {
		if _, err := New(f).Get(context.Background(), id); err != ErrInvalid {
			t.Fatal("invalid id")
		}
	}
	if f.calls != 0 {
		t.Fatal("invalid get reached store")
	}
}
