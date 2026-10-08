// 業務入力の正規化/拒否を偽Storeで確認する単体試験。internal/account/account.goのDB前検証を守る。
package account

import (
	"context"
	"testing"
)

type fake struct{ input Input }

func (f *fake) List(context.Context) ([]Account, error)     { return nil, nil }
func (f *fake) Get(context.Context, int64) (Account, error) { return Account{}, nil }
func (f *fake) Create(_ context.Context, i Input) (Account, error) {
	f.input = i
	return Account{}, nil
}
func (f *fake) Update(_ context.Context, _ int64, i Input) (Account, error) {
	f.input = i
	return Account{}, nil
}
func (f *fake) Delete(context.Context, int64) error { return nil }
func TestValidation(t *testing.T) {
	store := &fake{}
	if _, err := New(store).Create(context.Background(), Input{Name: " Test ", Email: " TEST@EXAMPLE.INVALID "}); err != nil {
		t.Fatal(err)
	}
	if store.input.Name != "Test" || store.input.Email != "test@example.invalid" {
		t.Fatalf("unexpected normalization: %+v", store.input)
	}
	if _, err := New(store).Create(context.Background(), Input{Name: "", Email: "bad"}); err != ErrInvalid {
		t.Fatalf("expected ErrInvalid, got %v", err)
	}
}
