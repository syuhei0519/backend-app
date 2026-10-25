// Package dbwait は、各サービスの DB 移行前に接続が使えるまで待つ処理をまとめる。
package dbwait

import (
	"context"
	"fmt"
	"time"
)

// Wait は DB の起動待ちを行う。DB が使えないまま処理が止まり続けないよう、
// 接続確認は最大10回、1回5秒まで、再試行の間隔は2秒に制限する。
// 呼び出し元の期限や中断も受け継ぐため、DB 移行全体の制限時間を超えて待たない。
func Wait(ctx context.Context, ping func(context.Context) error) error {
	return waitForDatabase(ctx, ping, waitRetry)
}

// pause を差し替えられるため、テストでは実際に待たずに再試行回数を確認できる。
func waitForDatabase(ctx context.Context, ping func(context.Context) error, pause func(context.Context) error) error {
	for attempt := 1; attempt <= 10; attempt++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("wait for database: %w", err)
		}
		trial, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := ping(trial)
		// 接続確認が終わるたびにタイマーを解放する。defer だと関数の終了まで残ってしまう。
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
	// 接続エラーにはパスワードなどが含まれる可能性があるため、元のエラーは返さない。
	return fmt.Errorf("wait for database: exhausted 10 bounded attempts")
}

// 待機中でも呼び出し元が中断したらすぐ戻るため、単純な Sleep は使わない。
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
