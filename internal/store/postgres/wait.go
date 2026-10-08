// migrationのDB接続待機。migrate.goがPingと待機関数を渡す。
// 成功ならnil、10試行失敗またはcontext中断ならエラー。wait_test.goは実DBなしで有限待機を確認する。
package postgres

import (
	"context"
	"fmt"
	"time"
)

// 最大10回、各Pingは5秒、試行間は2秒。さらにmigration全体170秒の親期限にも従う。
func waitForDatabase(ctx context.Context, ping func(context.Context) error, pause func(context.Context) error) error {
	for attempt := 1; attempt <= 10; attempt++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("wait for database: %w", err)
		}
		trial, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := ping(trial)
		// 各試行が終わった時点でタイマーを解放する。ループ内のdeferで終了まで溜めない。
		cancel()
		if err == nil {
			return nil
		}
		if attempt < 10 {
			if err := pause(ctx); err != nil {
				return fmt.Errorf("wait for database: %w", err)
			}
		}
	}
	// 接続文字列や資格情報を含み得るdriverエラーを、そのままログへ転記しない。
	return fmt.Errorf("wait for database: exhausted 10 bounded attempts")
}

// 中断可能なタイマー待機。単純なSleepとは異なり親context終了で直ちに戻る。
func waitRetry(ctx context.Context) error {
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
