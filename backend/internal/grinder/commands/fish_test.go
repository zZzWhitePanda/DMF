package commands

import (
	"testing"
	"time"

	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/discord"
)

func withViewer() *Fish {
	return &Fish{bucketViewerMsgID: "viewer", bucketViewerRefreshCustom: "refresh"}
}

// The saved menu keeps being used, and nothing drastic happens early.
func TestBucketKeepsTheSameMenuAtFirst(t *testing.T) {
	f := withViewer()
	start := time.Now()
	for _, after := range []time.Duration{0, 20 * time.Second, 40 * time.Second, 59 * time.Second} {
		if got := f.nextBucketStep(start.Add(after), true); got != bucketRefresh {
			t.Fatalf("at %s: got step %d, want refresh", after, got)
		}
	}
	if f.bucketViewerMsgID == "" {
		t.Fatal("the saved menu was thrown away")
	}
}

func TestBucketRecoveryFollowsTheClock(t *testing.T) {
	f := withViewer()
	start := time.Now()
	cases := []struct {
		after time.Duration
		want  bucketStep
	}{
		{0, bucketRefresh},
		{replaceViewerAfter, bucketOpen},
		{2 * time.Minute, bucketWait},
		{bucketStuckAfter - time.Second, bucketWait},
		{bucketStuckAfter, bucketRestart},
	}
	for _, c := range cases {
		if got := f.nextBucketStep(start.Add(c.after), true); got != c.want {
			t.Fatalf("at %s: got step %d, want %d", c.after, got, c.want)
		}
	}
	if !f.wantBuckets {
		t.Fatal("restart should send the next main menu to Buckets")
	}
}

// Each new menu shows in the channel as "used /fish catch", so a stuck bucket
// must not post one every attempt.
func TestStuckBucketOpensOneNewMenu(t *testing.T) {
	f := &Fish{}
	start := time.Now()
	opened := 0
	for at := time.Duration(0); at < bucketStuckAfter; at += bucketRetryGap {
		if f.nextBucketStep(start.Add(at), true) == bucketOpen {
			opened++
		}
		if opened == 1 && f.bucketViewerMsgID == "" {
			f.bucketViewerMsgID, f.bucketViewerRefreshCustom = "new", "refresh"
		}
	}
	if opened != 1 {
		t.Fatalf("opened %d menus, want 1", opened)
	}
}

func TestNewBucketGetsANewMenuAgain(t *testing.T) {
	f := &Fish{}
	now := time.Now()
	f.nextBucketStep(now, true)
	f.bucketSold()
	if got := f.nextBucketStep(now.Add(time.Hour), true); got != bucketOpen {
		t.Fatalf("got step %d, want open", got)
	}
}

// No screen with a Buckets button is not a reason to rush to /fish catch.
func TestNoBucketsButtonStillWaitsThreeMinutes(t *testing.T) {
	f := &Fish{}
	start := time.Now()
	if got := f.nextBucketStep(start, false); got != bucketWait {
		t.Fatalf("got step %d, want wait", got)
	}
	if got := f.nextBucketStep(start.Add(bucketStuckAfter-time.Second), false); got != bucketWait {
		t.Fatalf("got step %d, want wait", got)
	}
	if got := f.nextBucketStep(start.Add(bucketStuckAfter), false); got != bucketRestart {
		t.Fatalf("got step %d, want restart", got)
	}
}

// Past failures must not count against the next bucket.
func TestClockResetsWhenTheBucketClears(t *testing.T) {
	for name, clear := range map[string]func(*Fish){"sold": (*Fish).bucketSold, "room again": (*Fish).bucketNotFull} {
		t.Run(name, func(t *testing.T) {
			f := withViewer()
			start := time.Now()
			f.nextBucketStep(start, true)
			f.bucketViewerMsgID, f.bucketViewerRefreshCustom = "viewer", "refresh"
			clear(f)
			if got := f.nextBucketStep(start.Add(time.Hour), true); got != bucketRefresh {
				t.Fatalf("got step %d, want refresh", got)
			}
		})
	}
}

func TestRestartRespectsTheCommandGap(t *testing.T) {
	f := withViewer()
	start := time.Now()
	f.lastFishCommand = start
	f.nextBucketStep(start, true)
	if got := f.nextBucketStep(start.Add(bucketStuckAfter-time.Second), true); got == bucketRestart {
		t.Fatal("restarted inside the gap")
	}
	f.lastFishCommand = start.Add(time.Minute)
	if got := f.nextBucketStep(start.Add(bucketStuckAfter), true); got == bucketRestart {
		t.Fatal("restarted inside the gap")
	}
}

func TestFishCommandsAreRateLimited(t *testing.T) {
	f := &Fish{}
	now := time.Now()
	if !f.fishCommandAllowed(now) {
		t.Fatal("first command refused")
	}
	for _, after := range []time.Duration{0, time.Second, fishCommandGap - time.Second} {
		if f.fishCommandAllowed(now.Add(after)) {
			t.Fatalf("allowed a second command after %s", after)
		}
	}
	if !f.fishCommandAllowed(now.Add(fishCommandGap)) {
		t.Fatal("refused a command after the gap")
	}
}

// A "Hold Tight" brings the main loop back to Run every 30 seconds. It must
// not stack /fish catch commands on a session that's working.
func TestMainLoopLeavesALiveSessionAlone(t *testing.T) {
	f := &Fish{}
	now := time.Now()
	if !f.shouldInvoke(now) {
		t.Fatal("the first /fish catch was refused")
	}
	for wait := 30 * time.Second; wait < fishCommandGap; wait += 30 * time.Second {
		if f.shouldInvoke(now.Add(wait)) {
			t.Fatalf("invoked again after %s", wait)
		}
	}
	f.lastResponse = now.Add(5 * time.Minute)
	if f.shouldInvoke(now.Add(6 * time.Minute)) {
		t.Fatal("invoked on a session that just responded")
	}
	if !f.shouldInvoke(now.Add(9 * time.Minute)) {
		t.Fatal("refused to restart a session that went quiet")
	}
}

func TestBucketAttemptsAreSpacedOut(t *testing.T) {
	f := &Fish{}
	now := time.Now()
	if !f.bucketTryDue(now) {
		t.Fatal("first attempt refused")
	}
	f.lastBucketTry = now
	if f.bucketTryDue(now.Add(time.Second)) {
		t.Fatal("a replay right after an attempt should not retry")
	}
	if !f.bucketTryDue(now.Add(bucketRetryGap)) {
		t.Fatal("should try again after the gap")
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
