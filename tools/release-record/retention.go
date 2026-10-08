// release証跡の保持計画を返す。最新10件と90日、稼働中/rollback候補のdigestを保護する設計。
// 計画の返却は削除実行や現在のサーバー保持設定の保証ではない。retention_test.goで境界を確認する。
package release

import (
	"sort"
	"time"
)

// この統合ではGeneric Packageを自動削除しない。plannerは運用者レビュー後のcleanupの前提で、暗黙の削除ではない。1 releaseはdigest version内の全不変scan runをまとめた単位。
type RetainedRelease struct {
	PackageID  int64
	Digest     string
	CreatedAt  time.Time
	LastRunAt  time.Time
	RunsListed bool
}

type ProtectedDigest struct {
	Digest string
	Reason string // running or rollback, from the fixed inventory/explicit register
}

type RetentionDecision struct {
	PackageID int64  `json:"packageId"`
	Digest    string `json:"digest"`
	Keep      bool   `json:"keep"`
	Reason    string `json:"reason"`
}

// 完全かつ最新のinventory/Package一覧を入力にする。不明/未来/重複/不完全ならcleanup全体を止める。直近の失敗rescanもdigest version全体をさらに90日保持する理由になる。
func PlanRetention(releases []RetainedRelease, protected []ProtectedDigest, now time.Time) ([]RetentionDecision, error) {
	if now.IsZero() || len(protected) == 0 {
		return nil, ErrRefused
	}
	protectedSet := map[string]string{}
	for _, p := range protected {
		if !digestPattern.MatchString(p.Digest) || (p.Reason != "running" && p.Reason != "rollback") {
			return nil, ErrRefused
		}
		if protectedSet[p.Digest] != "running" {
			protectedSet[p.Digest] = p.Reason
		}
	}
	ordered := append([]RetainedRelease(nil), releases...)
	ids, digests := map[int64]bool{}, map[string]bool{}
	for _, r := range ordered {
		if r.PackageID <= 0 || ids[r.PackageID] || !digestPattern.MatchString(r.Digest) || digests[r.Digest] || r.CreatedAt.IsZero() || r.LastRunAt.IsZero() || r.CreatedAt.After(now) || r.LastRunAt.After(now) || r.LastRunAt.Before(r.CreatedAt) || !r.RunsListed {
			return nil, ErrRefused
		}
		ids[r.PackageID], digests[r.Digest] = true, true
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].CreatedAt.Equal(ordered[j].CreatedAt) {
			return ordered[i].PackageID > ordered[j].PackageID
		}
		return ordered[i].CreatedAt.After(ordered[j].CreatedAt)
	})
	result := make([]RetentionDecision, 0, len(ordered))
	for i, r := range ordered {
		reason := "eligible-after-review"
		if p, ok := protectedSet[r.Digest]; ok {
			reason = p
		} else if i < 10 {
			reason = "latest-ten"
		} else if now.Sub(r.LastRunAt) <= 90*24*time.Hour {
			reason = "ninety-days"
		}
		result = append(result, RetentionDecision{r.PackageID, r.Digest, reason != "eligible-after-review", reason})
	}
	return result, nil
}
