package commands

import (
	"testing"
	"time"

	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/discord"
)

func withViewer() *Fish {
	return &Fish{bucketViewerMsgID: "viewer", bucketViewerRefreshCustom: "refresh"}
}

// A dead viewer must be replaced, then the whole menu, never retried forever.
func TestBucketRecoveryClimbsTheLadder(t *testing.T) {
	f := withViewer()
	now := time.Now()
	want := []bucketStep{bucketRefresh, bucketOpen, bucketRestart, bucketOpen, bucketOpen, bucketOpen}
	for n, step := range want {
		if got := f.nextBucketStep(now, true); got != step {
			t.Fatalf("attempt %d: got step %d, want %d", n+1, got, step)
		}
		now = now.Add(time.Minute)
	}
	if !f.wantBuckets {
		t.Fatal("restart should send the next main menu to Buckets")
	}
	if f.bucketViewerMsgID != "" {
		t.Fatal("the dead viewer was kept")
	}
}

func TestBucketRecoveryRestartsAgainAfterTheCooldown(t *testing.T) {
	f := withViewer()
	now := time.Now()
	f.nextBucketStep(now, true)
	f.nextBucketStep(now, true)
	if got := f.nextBucketStep(now, true); got != bucketRestart {
		t.Fatalf("got step %d, want restart", got)
	}
	later := now.Add(restartCooldown)
	for n := 0; n < restartAfterAttempts-1; n++ {
		f.nextBucketStep(later, true)
	}
	if got := f.nextBucketStep(later, true); got != bucketRestart {
		t.Fatalf("got step %d, want a second restart", got)
	}
}

func TestBucketRecoveryWithNoButtonRestarts(t *testing.T) {
	f := &Fish{}
	now := time.Now()
	if got := f.nextBucketStep(now, false); got != bucketRestart {
		t.Fatalf("got step %d, want restart", got)
	}
	if got := f.nextBucketStep(now, false); got != bucketWait {
		t.Fatalf("during the cooldown got step %d, want wait", got)
	}
}

func TestSaleResetsTheLadder(t *testing.T) {
	f := withViewer()
	now := time.Now()
	f.nextBucketStep(now, true)
	f.bucketViewerMsgID, f.bucketViewerRefreshCustom = "new", "refresh"
	f.bucketSold()
	if got := f.nextBucketStep(now, true); got != bucketRefresh {
		t.Fatalf("after a sale got step %d, want refresh", got)
	}
}

// A repeat of a fish seen before looks like a replay. It must not hide a full
// bucket once the last attempt is old.
func TestBucketTryDue(t *testing.T) {
	f := &Fish{}
	now := time.Now()
	if !f.bucketTryDue(now, false) {
		t.Fatal("a new catch should try")
	}
	f.lastBucketTry = now
	if f.bucketTryDue(now.Add(time.Second), true) {
		t.Fatal("a replay right after an attempt should not retry")
	}
	if !f.bucketTryDue(now.Add(bucketRetryGap), true) {
		t.Fatal("a repeat fish after the gap should try")
	}
}

func TestFindBucketsButton(t *testing.T) {
	btns := []*discord.MessageComponent{
		{Label: "Go Fishing", CustomID: "fish"},
		{Label: "View Buckets", CustomID: "buckets"},
	}
	if b := findBucketsButton(btns); b == nil || b.CustomID != "buckets" {
		t.Fatalf("got %+v", b)
	}
	btns[1].Disabled = true
	if b := findBucketsButton(btns); b != nil {
		t.Fatalf("picked a disabled button: %+v", b)
	}
	if goFishingCustom(btns) != "fish" {
		t.Fatal("Go Fishing not found")
	}
}
