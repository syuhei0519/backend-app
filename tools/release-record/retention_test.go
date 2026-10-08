// 保持計画の最新件数・90日・稼働中/rollback保護の境界試験。サーバーで実際に削除を実行する試験ではない。
package release

import (
	"fmt"
	"testing"
	"time"
)

func TestRetentionUnionAndLiveRollbackExclusions(t *testing.T) {
	var releases []RetainedRelease
	for i := 1; i <= 16; i++ {
		tm := at.Add(-time.Duration(100+i) * 24 * time.Hour)
		releases = append(releases, RetainedRelease{int64(i), fmt.Sprintf("sha256:%064x", i), tm, tm, true})
	}
	protected := []ProtectedDigest{{releases[11].Digest, "running"}, {releases[12].Digest, "rollback"}}
	releases[10].LastRunAt = at.Add(-90 * 24 * time.Hour) // exact boundary kept
	releases[13].LastRunAt = at.Add(-time.Hour)           // failed/second rescan is kept
	plan, err := PlanRetention(releases, protected, at)
	if err != nil || len(plan) != 16 {
		t.Fatal("complete retention input refused")
	}
	for i, p := range plan {
		wantKeep := i < 14
		if p.Keep != wantKeep {
			t.Fatal("latest10 OR90days ORlive/rollback retention lost")
		}
	}
	releases = releases[:9]
	plan, err = PlanRetention(releases, protected, at)
	if err != nil {
		t.Fatal("under10 releases refused")
	}
	for _, p := range plan {
		if !p.Keep {
			t.Fatal("fewer than10 releases deleted")
		}
	}
}

func TestUnknownRetentionInputsBlockWholeCleanup(t *testing.T) {
	valid := RetainedRelease{1, run().Digest, at.Add(-100 * 24 * time.Hour), at.Add(-100 * 24 * time.Hour), true}
	protected := []ProtectedDigest{{run().Digest, "running"}}
	for _, change := range []func(*RetainedRelease){
		func(r *RetainedRelease) { r.RunsListed = false },
		func(r *RetainedRelease) { r.CreatedAt = time.Time{} },
		func(r *RetainedRelease) { r.LastRunAt = at.Add(time.Second) },
		func(r *RetainedRelease) { r.LastRunAt = r.CreatedAt.Add(-time.Second) },
		func(r *RetainedRelease) { r.Digest = "sha256:unknown" },
	} {
		bad := valid
		change(&bad)
		if plan, err := PlanRetention([]RetainedRelease{bad}, protected, at); err == nil || plan != nil {
			t.Fatal("unknown/future/incomplete input permitted cleanup")
		}
	}
	if plan, err := PlanRetention([]RetainedRelease{valid, valid}, protected, at); err == nil || plan != nil {
		t.Fatal("duplicate package/digest permitted cleanup")
	}
	if _, err := PlanRetention([]RetainedRelease{valid}, nil, at); err == nil {
		t.Fatal("absent running/rollback inventory permitted cleanup")
	}
}
