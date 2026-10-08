// DB接続を実行せず、注入したPing/pauseで有限待機・復旧・中断を確認する。
// 待機成功は実DB migration/lockの受入成功の代用ではない。
package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// 10回で失敗し、待機が9回で止まることを確認。DB障害でJobを無期限に占有する退行を防ぐ。
func TestWaitExhaustsTenBoundedAttempts(t *testing.T) {
	calls, pauses := 0, 0
	var trials []context.Context
	err := waitForDatabase(context.Background(), func(ctx context.Context) error {
		calls++
		trials = append(trials, ctx)
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 5*time.Second {
			t.Fatal("unbounded trial")
		}
		return errors.New("private-dsn-password")
	}, func(context.Context) error { pauses++; return nil })
	if calls != 10 || pauses != 9 || err == nil || strings.Contains(err.Error(), "private-dsn") {
		t.Fatalf("calls=%d pauses=%d err=%v", calls, pauses, err)
	}
	for _, ctx := range trials {
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatal("trial was not released")
		}
	}
}

// 途中復旧なら成功し、残りの不要な接続試行をしないことを確認する。
func TestWaitRecoversBeforeAttemptLimit(t *testing.T) {
	calls := 0
	err := waitForDatabase(context.Background(), func(context.Context) error {
		calls++
		if calls == 3 {
			return nil
		}
		return errors.New("not ready")
	}, func(context.Context) error { return nil })
	if err != nil || calls != 3 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

// 親context中断後に次の接続試行へ進まないことを確認する。
func TestWaitCancellationStopsBeforeAnotherConnection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := waitForDatabase(ctx, func(context.Context) error { calls++; return errors.New("unavailable") }, func(ctx context.Context) error { cancel(); return ctx.Err() })
	if calls != 1 || !errors.Is(err, context.Canceled) {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestWaitHonorsShortParentDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	calls := 0
	err := waitForDatabase(ctx, func(trial context.Context) error { calls++; <-trial.Done(); return trial.Err() }, waitRetry)
	if calls != 1 || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}
