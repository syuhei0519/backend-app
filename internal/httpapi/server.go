// HTTPルート・入力境界・エラー応答を担当。cmd/server/main.goが業務Service/ready判定/loggerを注入する。
// frontend-app/src/api/accounts.tsからの/api/accounts要求をJSONへ変換し、業務層へcontextを渡す。
package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"gitlab.com/platform-engineering-lab/backend-app/internal/account"
	"gitlab.com/platform-engineering-lab/backend-app/internal/telemetry"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
)

type service interface {
	List(context.Context) ([]account.Account, error)
	Get(context.Context, int64) (account.Account, error)
	Create(context.Context, account.Input) (account.Account, error)
	Update(context.Context, int64, account.Input) (account.Account, error)
	Delete(context.Context, int64) error
}
type readiness interface{ Ready(context.Context) error }
type Server struct {
	http.Handler
	ready atomic.Bool
}

func New(svc service, check readiness, logger *slog.Logger) *Server {
	return NewWithTelemetry(svc, check, logger, nil)
}

// ハンドラ構築。requestID→observe→本文上限→mux→業務処理の順に通る。
// 認証・利用者別認可の実装はここにはないため、入力検証を認証保証とは扱わない。
func NewWithTelemetry(svc service, check readiness, logger *slog.Logger, observation *telemetry.Telemetry) *Server {
	s := &Server{}
	s.ready.Store(true)
	mux := http.NewServeMux()
	// livezはプロセス応答のみ、readyzは終了状態とschema互換性を確認する。DB障害をlivenessで再起動し続けない。
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) { write(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if !s.ready.Load() || check.Ready(r.Context()) != nil {
			write(w, 503, map[string]string{"status": "unavailable"})
			return
		}
		write(w, 200, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/accounts", func(w http.ResponseWriter, r *http.Request) {
		items, err := svc.List(r.Context())
		respond(w, items, err, 200)
	})
	mux.HandleFunc("POST /api/accounts", func(w http.ResponseWriter, r *http.Request) {
		input, ok := decode(w, r)
		if !ok {
			return
		}
		item, err := svc.Create(r.Context(), input)
		respond(w, item, err, 201)
	})
	mux.HandleFunc("GET /api/accounts/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := id(w, r)
		if !ok {
			return
		}
		item, err := svc.Get(r.Context(), id)
		respond(w, item, err, 200)
	})
	mux.HandleFunc("PUT /api/accounts/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := id(w, r)
		if !ok {
			return
		}
		input, ok := decode(w, r)
		if !ok {
			return
		}
		item, err := svc.Update(r.Context(), id, input)
		respond(w, item, err, 200)
	})
	mux.HandleFunc("DELETE /api/accounts/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := id(w, r)
		if !ok {
			return
		}
		if err := svc.Delete(r.Context(), id); err != nil {
			failure(w, err)
			return
		}
		w.WriteHeader(204)
	})
	s.Handler = requestID(observe(logger, mux, observation))
	return s
}

// atomic.Boolで終了状態を並行HTTPハンドラへ安全に公開。readyzを503へ切り替える。
func (s *Server) BeginShutdown() { s.ready.Store(false) }

// パスIDは正のint64のみ。検証失敗は400としてここで要求を打ち切る。
func id(w http.ResponseWriter, r *http.Request) (int64, bool) {
	value, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || value < 1 {
		failure(w, account.ErrInvalid)
		return 0, false
	}
	return value, true
}

// 未知フィールドと、JSONの後ろに連結された追加データを拒否。入力の意味の検証はaccount.validateで行う。
func decode(w http.ResponseWriter, r *http.Request) (account.Input, bool) {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var input account.Input
	if err := decoder.Decode(&input); err != nil {
		failure(w, account.ErrInvalid)
		return input, false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		failure(w, account.ErrInvalid)
		return input, false
	}
	return input, true
}
func respond(w http.ResponseWriter, value any, err error, status int) {
	if err != nil {
		failure(w, err)
		return
	}
	write(w, status, value)
}

// 業務エラーを400/404/409/503へ変換し、内部エラーの詳細は応答へ含めない。
func failure(w http.ResponseWriter, err error) {
	status, code, message := 500, "internal_error", "サーバーで問題が発生しました。"
	switch {
	case errors.Is(err, account.ErrInvalid):
		status, code, message = 400, "validation_error", "入力内容を確認してください。"
	case errors.Is(err, account.ErrNotFound):
		status, code, message = 404, "not_found", "アカウントが見つかりません。"
	case errors.Is(err, account.ErrConflict):
		status, code, message = 409, "email_exists", "メールアドレスは登録済みです。"
	case errors.Is(err, account.ErrUnavailable):
		status, code, message = 503, "unavailable", "一時的に利用できません。"
	}
	write(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func write(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// 本文を1MiBまでに制限して無制限読み込みを避ける。超過時のdecode失敗も現実装では400へまとめる。
func limit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		next.ServeHTTP(w, r)
	})
}

type key string

const requestKey key = "request_id"

var safeRequestID = regexp.MustCompile(`^(?:[a-fA-F0-9]{32}|[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12})$`)

// 外部X-Request-IDは許可形式だけ受け入れ、それ以外は生成。contextに保持し応答ヘッダー・ログへ渡す。
// request_idはtrace_idとは独立の値。乱数生成のerrは現実装では無視している。
func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if !safeRequestID.MatchString(value) {
			data := make([]byte, 16)
			_, _ = rand.Read(data)
			value = hex.EncodeToString(data)
		}
		w.Header().Set("X-Request-ID", value)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestKey, value)))
	})
}

type recorder struct {
	http.ResponseWriter
	status  int
	written bool
}

// 最初の最終statusを記録する。1xxは最終statusにせず、二重WriteHeaderによる上書きを防ぐ。
func (r *recorder) WriteHeader(status int) {
	if status < 200 {
		r.ResponseWriter.WriteHeader(status)
		return
	}
	if r.written {
		return
	}
	r.status, r.written = status, true
	r.ResponseWriter.WriteHeader(status)
}
func (r *recorder) Write(b []byte) (int, error) {
	if !r.written {
		r.WriteHeader(200)
	}
	return r.ResponseWriter.Write(b)
}
func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
