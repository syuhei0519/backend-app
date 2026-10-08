// HTTPとDBの間の業務層。internal/httpapi/server.goからInput/ID/contextを受ける。
// 入力を正規化してStoreへ渡し、Accountまたは分類済みエラーを返す。DB実装はinternal/store/postgres/store.go。
package account

import (
	"context"
	"errors"
	"net/mail"
	"strings"
	"time"
)

var (
	ErrInvalid     = errors.New("invalid account")
	ErrNotFound    = errors.New("account not found")
	ErrConflict    = errors.New("email already exists")
	ErrUnavailable = errors.New("store unavailable")
)

type Account struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
type Input struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// interfaceで業務層をPostgreSQL実装から分離する。テストでは偽Storeを渡して検証できる。
type Store interface {
	List(context.Context) ([]Account, error)
	Get(context.Context, int64) (Account, error)
	Create(context.Context, Input) (Account, error)
	Update(context.Context, int64, Input) (Account, error)
	Delete(context.Context, int64) error
}
type Service struct{ store Store }

// Storeへの委譲入口。参照・削除はそのまま渡し、作成・更新だけ下で入力を検証する。
func New(store Store) *Service                                        { return &Service{store: store} }
func (s *Service) List(ctx context.Context) ([]Account, error)        { return s.store.List(ctx) }
func (s *Service) Get(ctx context.Context, id int64) (Account, error) { return s.store.Get(ctx, id) }
func (s *Service) Delete(ctx context.Context, id int64) error         { return s.store.Delete(ctx, id) }

// 検証に失敗すればDBへ書かない。成功後の一意性判定はDBのunique indexが担う。
func (s *Service) Create(ctx context.Context, input Input) (Account, error) {
	value, err := validate(input)
	if err != nil {
		return Account{}, err
	}
	return s.store.Create(ctx, value)
}

// 更新も作成と同じ検証を通す。存在しないIDの判定はStoreへ委譲する。
func (s *Service) Update(ctx context.Context, id int64, input Input) (Account, error) {
	value, err := validate(input)
	if err != nil {
		return Account{}, err
	}
	return s.store.Update(ctx, id, value)
}

// 前後空白を除きemailを小文字へ正規化する。表示名付きメール表記は受け付けない。
// Goのlen(string)は文字数ではなくバイト数。制限はブラウザ側のmaxLengthと完全同一の尺度ではない。
func validate(input Input) (Input, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	address, err := mail.ParseAddress(input.Email)
	if input.Name == "" || len(input.Name) > 100 || err != nil || address.Address != input.Email || len(input.Email) > 254 {
		return Input{}, ErrInvalid
	}
	return input, nil
}
