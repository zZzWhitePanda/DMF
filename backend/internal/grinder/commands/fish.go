package commands

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/webp" // Discord CDN serves grid images as WEBP
	"io"
	"math/rand"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/discord"
	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/events"
	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/instance"
)

// Fish drives Dank Memer's /fish catch loop:
//   main menu → Go Fishing → catch grid → caught fish → Sell Creature →
//   Pending Confirmation → Confirm → Fish Again → back to catch grid.
//
// Fish is EXCLUSIVE — when enabled, other commands are ignored (same as Flow).
// The catch grid image gets downloaded, saved to .debug/fish/ for inspection,
// and analyzed for the darkest 3x3 cell (best-guess "where the fish is").
type Fish struct {
	mu            sync.Mutex
	soldMessages  map[string]bool // message IDs we've already clicked Sell on (reset per new catch)
	seenCatches   map[string]bool // keyed by fish_name|size — DM edits one message across catches so m.ID alone can't tell them apart
	sellingBucket bool            // currently in bucket-overflow sell flow

	// When we trigger bucket-overflow (click Open Buckets on the caught-fish
	// screen), remember the parent catch message so after bucket is sold we
	// can click its Fish Again button directly — re-invoking /fish catch
	// creates a second active command and DM blocks us with Hold Tight.
	parentCatchMsgID      string
	parentFishAgainCustom string

	// Remember the first bucket viewer message so subsequent bucket-full
	// cycles can REFRESH it in place (keeps chat at 2 messages) instead of
	// opening a brand new bucket viewer every time.
	bucketViewerMsgID         string
	bucketViewerRefreshCustom string

	// Bucket-full recovery runs on a clock, not a count: a catch screen arrives
	// several times over, so counting them looks like failure when it isn't.
	bucketFullSince time.Time // when this bucket filled, zero if it isn't full
	lastBucketTry   time.Time
	lastFishCommand time.Time // last /fish catch, however it was triggered
	lastResponse    time.Time // last message handled, so a live session can be told apart
	wantBuckets     bool      // the next main menu should open Buckets, not fish
	openedViewer    bool      // already opened a new menu for this full bucket
}

const (
	// A catch screen arrives several times (create, update, resync), so
	// attempts closer together than this are the same attempt.
	bucketRetryGap = 30 * time.Second
	// How long the saved bucket menu gets before a new one is opened.
	replaceViewerAfter = time.Minute
	// How long a full bucket must go unsold before /fish catch runs again.
	bucketStuckAfter = 3 * time.Minute
	// Hard floor between /fish catch commands, whatever asks for one.
	fishCommandGap = 3 * time.Minute
)

type bucketStep int

const (
	bucketRefresh bucketStep = iota // refresh the viewer we already have
	bucketOpen                      // open a new viewer from the catch screen
	bucketRestart                   // run /fish catch, then open Buckets from its menu
	bucketWait                      // nothing to try yet, keep fishing
)

// nextBucketStep picks what to do about a full bucket, based on how long it
// has gone unsold. The saved menu is used for as long as it might work, and
// /fish catch only runs after bucketStuckAfter of real failure. canOpen is
// whether this screen has a Buckets button.
func (f *Fish) nextBucketStep(now time.Time, canOpen bool) bucketStep {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.bucketFullSince.IsZero() {
		f.bucketFullSince = now
	}
	f.lastBucketTry = now
	unsold := now.Sub(f.bucketFullSince)
	if unsold >= bucketStuckAfter && now.Sub(f.lastFishCommand) >= fishCommandGap {
		f.bucketFullSince = now
		f.openedViewer = false
		f.wantBuckets = true
		f.parentCatchMsgID = ""
		f.parentFishAgainCustom = ""
		f.forgetViewerLocked()
		return bucketRestart
	}
	if unsold < replaceViewerAfter && f.bucketViewerMsgID != "" && f.bucketViewerRefreshCustom != "" {
		return bucketRefresh
	}
	// Opening a menu posts a new message, which Discord labels "used /fish
	// catch", so this happens once per full bucket. Refreshing doesn't post.
	if canOpen && !f.openedViewer {
		f.openedViewer = true
		f.forgetViewerLocked()
		return bucketOpen
	}
	if f.bucketViewerMsgID != "" && f.bucketViewerRefreshCustom != "" {
		return bucketRefresh
	}
	return bucketWait
}

func (f *Fish) forgetViewerLocked() {
	f.bucketViewerMsgID = ""
	f.bucketViewerRefreshCustom = ""
}

// bucketTryDue says whether this catch screen should start a bucket attempt.
// It goes by time alone: a repeat of a fish we've seen before looks like a
// replay, and skipping those is how the bot used to fish a full bucket forever.
func (f *Fish) bucketTryDue(now time.Time) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return now.Sub(f.lastBucketTry) >= bucketRetryGap
}

// bucketSold clears the recovery state once a sale goes through.
func (f *Fish) bucketSold() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bucketFullSince = time.Time{}
	f.openedViewer = false
	f.wantBuckets = false
}

// bucketNotFull clears the clock when a catch shows there's room again, such
// as after a sale made in Discord, so an old bucket can't look stuck.
func (f *Fish) bucketNotFull() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bucketFullSince = time.Time{}
	f.openedViewer = false
}

// fishCommandAllowed claims the next /fish catch. Every one goes through here,
// so nothing can run them faster than fishCommandGap.
func (f *Fish) fishCommandAllowed(now time.Time) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if now.Sub(f.lastFishCommand) < fishCommandGap {
		return false
	}
	f.lastFishCommand = now
	return true
}

// shouldInvoke is the main loop's gate for /fish catch: never while the
// session is still responding, and never faster than fishCommandGap.
func (f *Fish) shouldInvoke(now time.Time) bool {
	f.mu.Lock()
	alive := !f.lastResponse.IsZero() && now.Sub(f.lastResponse) < fishCommandGap
	f.mu.Unlock()
	return !alive && f.fishCommandAllowed(now)
}

// sendFishCatch is the only place /fish catch is sent from. It always says so,
// in Activity and the server log, along with why.
func sendFishCatch(i *instance.Instance, reason string) error {
	i.Log.WithField("reason", reason).Info("fish catch sent")
	i.Bus.Send(events.Event{Kind: events.Warn, Message: "sent /fish catch: " + reason})
	return i.InvokeSlashCommand("fish", []map[string]interface{}{{"type": 1, "name": "catch"}})
}

// IgnoresCooldownText tells the instance that cooldown-sounding text isn't a
// reason to back off. Fishing screens can mention waiting, and treating one as
// a cooldown both drops the screen and shortens the 24 hour cooldown.
func (f *Fish) IgnoresCooldownText() bool { return true }

// OnStuck runs when Dank Memer has gone quiet for 3 minutes.
func (f *Fish) OnStuck(i *instance.Instance) {
	if !f.fishCommandAllowed(time.Now()) {
		return
	}
	if err := sendFishCatch(i, "no Dank Memer activity for 3 minutes"); err != nil {
		i.Bus.Send(events.Event{Kind: events.Warn, Message: fmt.Sprintf("restart fish catch failed: %v", err)})
	}
}

// resumeFishing runs /fish catch to get going again after a sale, if the rate
// limit allows it. If not, the stuck detector picks the session back up.
func (f *Fish) resumeFishing(i *instance.Instance) {
	if !f.fishCommandAllowed(time.Now()) {
		return
	}
	if err := sendFishCatch(i, "resuming after a bucket sale"); err != nil {
		i.Bus.Send(events.Event{Kind: events.Warn, Message: fmt.Sprintf("resume fish catch failed: %v", err)})
	}
}

func (f *Fish) restartFishing(i *instance.Instance) error {
	if !f.fishCommandAllowed(time.Now()) {
		return nil
	}
	time.Sleep(humanDelay())
	return sendFishCatch(i, "bucket unsold for 3 minutes")
}

func (f *Fish) Name() string { return "fish" }

// Effectively "never re-invoke" during a normal session. The /fish catch
// parent message stays live indefinitely as DM edits it on every click; we
// never need another one. Previous 10-minute re-invoke was creating a second
// (orphan) /fish catch message every time the loop re-picked Fish, which
// then confused state since button clicks still went to the original.
// If the flow genuinely dies (gateway disconnect missed updates, DM glitch),
// the user restarts via q → Start on the dashboard.
func (f *Fish) DefaultCooldown() time.Duration { return 24 * time.Hour }

func (f *Fish) Run(ctx context.Context, i *instance.Instance) (time.Duration, error) {
	f.mu.Lock()
	if f.soldMessages == nil {
		f.soldMessages = make(map[string]bool)
	}
	if f.seenCatches == nil {
		f.seenCatches = make(map[string]bool)
	}
	if len(f.soldMessages) > 100 {
		f.soldMessages = make(map[string]bool)
	}
	if len(f.seenCatches) > 200 {
		f.seenCatches = make(map[string]bool)
	}
	f.mu.Unlock()

	// Run is meant to happen once, but any "Hold Tight" reply shortens this
	// command's cooldown to 30 seconds, which brings the main loop back here.
	// A second /fish catch earns another Hold Tight, so that loops forever
	// unless a live session is left alone.
	if !f.shouldInvoke(time.Now()) {
		return fishCommandGap, nil
	}
	if err := sendFishCatch(i, "starting fishing"); err != nil {
		return 60 * time.Second, err
	}
	return 24 * time.Hour, nil
}

var (
	fishCaughtRE = regexp.MustCompile(`(?i)you caught\s+(?:a |an |the )?([a-zA-Z][a-zA-Z\s'-]*?)[.!]`)
	rodBrokenRE  = regexp.MustCompile(`(?i)(?:your\s+)?([a-zA-Z\s]+?rod|harpoon|net|dynamite|fishing\s+bow|magnet\s+fishing\s+rope|bare\s+hand)\s+(?:broke|has\s+broken|is\s+destroyed|snapped)`)
	fishSizeRE   = regexp.MustCompile(`(?i)size:?\s*\**\s*(\d+(?:\.\d+)?\s*lbs?\s*\d+(?:\.\d+)?\s*in)`)
)

// Fish token patterns, tried in order. The "Successfully sold N fish for M"
// variant is the main one (bulk sell confirmation message); older patterns
// match per-fish sell outcomes.
var fishTokenPatterns = []*regexp.Regexp{
	// "Successfully sold 10 fish for <:fishtoken:..> 10"  — bulk mode
	regexp.MustCompile(`(?i)sold\s+\d+\s+fish\s+for\s+(?:<a?:[a-zA-Z0-9_]+:\d+>\s*)?(\d+)`),
	// "received 5 fish tokens" / "got 3 tokens"
	regexp.MustCompile(`(?i)(?:received|got|earned)\s+(?:<a?:[a-zA-Z0-9_]+:\d+>\s*)?(\d+)\s*(?:fish\s+)?tokens?`),
	// Loose fallback: fishtoken emoji followed by a number
	regexp.MustCompile(`(?i)<a?:fishtoken[a-z]*:\d+>\s*(\d+)`),
}

func extractFishTokens(text string) int {
	for _, re := range fishTokenPatterns {
		if m := re.FindStringSubmatch(text); m != nil {
			if n, err := parseIntSafe(m[1]); err == nil && n > 0 {
				return n
			}
		}
	}
	return 0
}

// extractCatchID returns a unique identifier for a catch (fish_name + size),
// used because DM EDITS one parent message across all catches so m.ID alone
// cannot distinguish one catch from the next.
func extractCatchID(text string) string {
	nameMatch := fishCaughtRE.FindStringSubmatch(text)
	if nameMatch == nil {
		return ""
	}
	name := strings.ToLower(strings.TrimSpace(nameMatch[1]))
	size := ""
	if sm := fishSizeRE.FindStringSubmatch(text); sm != nil {
		size = strings.ToLower(strings.TrimSpace(sm[1]))
	}
	return name + "|" + size
}

func (f *Fish) OnResponse(ctx context.Context, i *instance.Instance, m *discord.Message) error {
	// User paused via dashboard q/esc — stop reacting to messages until resumed.
	if i.State.IsPaused() {
		return nil
	}

	f.mu.Lock()
	f.lastResponse = time.Now()
	f.mu.Unlock()

	text := buildText(m)
	btns := discord.CollectButtons(m.Components)

	// Rod break detection — surface prominently then fall through.
	if match := rodBrokenRE.FindStringSubmatch(text); match != nil {
		rodName := strings.TrimSpace(match[1])
		i.Bus.Send(events.Event{
			Kind:    events.RodBroken,
			Command: "fish",
			Message: rodName,
		})
		i.Bus.Send(events.Event{
			Kind:    events.Warn,
			Message: fmt.Sprintf("rod broke: %s - open equipment menu to swap or buy new one", rodName),
		})
	}

	// Any ephemeral status popup (Successfully sold, Action Confirmed, Hold
	// Tight) with no interactive buttons. First extract fish token rewards
	// from the text, then attempt dismiss (silently — Discord's DELETE on
	// ephemerals always returns 404 "Unknown Message" because they're not
	// in the channel's message history; this is a Discord API limitation
	// for selfbots, not a bug we can fix).
	if m.Flags&64 != 0 && len(btns) == 0 {
		if n := extractFishTokens(text); n > 0 {
			i.Bus.Send(events.Event{Kind: events.FishToken, Reward: n})
		}
		go func(ch, msg string) {
			err := i.Rest.DeleteMessage(ch, msg)
			if err == nil {
				return
			}
			// 404 Unknown Message is EXPECTED for ephemerals — suppress.
			// Only log unexpected failure modes.
			if !strings.Contains(err.Error(), "404") {
				i.Bus.Send(events.Event{Kind: events.Warn, Message: fmt.Sprintf("dismiss ephemeral failed: %v", err)})
			}
		}(m.ChannelID, m.ID)
		return nil
	}

	// 1. Ephemeral sell-confirmation popup — click Confirm IMMEDIATELY, passing
	// the message's flags so Discord recognises the ephemeral interaction.
	if isSellConfirmation(text, btns) {
		for _, b := range btns {
			if strings.EqualFold(b.Label, "confirm") {
				i.Bus.Send(events.Event{Kind: events.Info, Message: fmt.Sprintf("sell confirm detected (flags=%d) - clicking", m.Flags)})
				err := i.ClickButtonOnMessage(m, b.CustomID)
				if err != nil {
					i.Bus.Send(events.Event{Kind: events.Err, Message: fmt.Sprintf("confirm click FAILED: %v", err)})
					return err
				}
				i.Bus.Send(events.Event{Kind: events.Success, Message: "confirm click accepted"})

				// Dismiss the confirmation popup after a short delay to let
				// DM post the "Successfully sold" follow-up (which we also
				// auto-dismiss above when it arrives).
				go func(ch, msg string) {
					time.Sleep(1500 * time.Millisecond)
					_ = i.Rest.DeleteMessage(ch, msg)
				}(m.ChannelID, m.ID)

				// If this confirm was for a bucket-overflow Sell All Fish,
				// the bucket viewer stays open with no way back to fishing.
				// Re-invoke /fish catch in the background to resume.
				f.mu.Lock()
				wasBucketSell := f.sellingBucket
				f.sellingBucket = false
				f.mu.Unlock()
				f.bucketSold()
				if wasBucketSell {
					go func() {
						time.Sleep(2 * time.Second)
						f.resumeFishing(i)
					}()
				}
				return nil
			}
		}
		i.Bus.Send(events.Event{Kind: events.Warn, Message: "sell confirm shown but Confirm button missing - likely timed out"})
		return nil
	}

	// 1b. Bucket-sell confirmation popup — title "Selling Creatures" with
	// buttons "Cancel", "Sell for N Coins", "Sell for N Tokens". Pick the
	// right one based on configured sell_mode.
	if isBucketSellConfirmation(text, btns) {
		sellMode := strings.ToLower(i.Cfg.Features.Fishing.SellMode)
		if sellMode == "" {
			sellMode = "fish_tokens"
		}
		var pick *discord.MessageComponent
		for idx, b := range btns {
			l := strings.ToLower(b.Label)
			if !strings.Contains(l, "sell for") {
				continue
			}
			if sellMode == "fish_tokens" && strings.Contains(l, "token") {
				pick = btns[idx]
				break
			}
			if sellMode == "coins" && strings.Contains(l, "coin") {
				pick = btns[idx]
				break
			}
		}
		if pick == nil {
			for idx, b := range btns {
				if strings.Contains(strings.ToLower(b.Label), "sell for") {
					pick = btns[idx]
					break
				}
			}
		}
		if pick != nil {
			i.Bus.Send(events.Event{Kind: events.Info, Message: fmt.Sprintf("bucket sell confirm (%s): clicking %q", sellMode, pick.Label)})
			err := i.ClickButtonOnMessage(m, pick.CustomID)
			if err != nil {
				i.Bus.Send(events.Event{Kind: events.Err, Message: fmt.Sprintf("bucket sell confirm click FAILED: %v", err)})
				return err
			}
			// Grab + clear parent catch message info. The parent /fish catch
			// message is still live and has a Fish Again button — we click
			// that to resume rather than spawning a duplicate /fish catch.
			f.mu.Lock()
			wasBucketSell := f.sellingBucket
			parentMsgID := f.parentCatchMsgID
			parentCustom := f.parentFishAgainCustom
			f.sellingBucket = false
			f.parentCatchMsgID = ""
			f.parentFishAgainCustom = ""
			f.mu.Unlock()
			f.bucketSold()
			if wasBucketSell && parentMsgID != "" && parentCustom != "" {
				go func() {
					time.Sleep(2 * time.Second)
					if err := i.ClickButton(parentMsgID, parentCustom); err != nil {
						i.Bus.Send(events.Event{Kind: events.Warn, Message: fmt.Sprintf("post-sell Fish Again click failed: %v", err)})
						f.resumeFishing(i)
					} else {
						i.Bus.Send(events.Event{Kind: events.Info, Message: "bucket sold, clicked Fish Again on parent to resume"})
					}
				}()
			} else if wasBucketSell {
				go func() {
					time.Sleep(2 * time.Second)
					f.resumeFishing(i)
				}()
			}
			return nil
		}
		return nil
	}

	// 2. Bucket viewer screen (reached via Open Buckets when bucket is full)
	// — click Sell All Fish to open the Selling Creatures confirmation.
	// Also capture this message's id + the Refresh button's custom_id so
	// subsequent bucket-full cycles can refresh THIS message in place
	// instead of spawning a new bucket viewer every time.
	if isBucketViewer(text, btns) {
		refreshCustom := findRefreshCustom(btns)
		f.mu.Lock()
		f.bucketViewerMsgID = m.ID
		if refreshCustom != "" {
			f.bucketViewerRefreshCustom = refreshCustom
		}
		alreadyClicked := f.soldMessages[m.ID]
		f.mu.Unlock()
		if alreadyClicked {
			return nil
		}
		for _, b := range btns {
			if strings.EqualFold(b.Label, "sell all fish") && !b.Disabled {
				i.Bus.Send(events.Event{Kind: events.Info, Message: "bucket viewer: clicking Sell All Fish"})
				f.mu.Lock()
				f.sellingBucket = true
				f.soldMessages[m.ID] = true
				f.mu.Unlock()
				return i.ClickButtonOnMessage(m, b.CustomID)
			}
		}
		return nil
	}

	// Diagnostic: if a message has Confirm+Cancel buttons but our detector
	// didn't match, log it so we can see what text format DM used.
	if hasBtn(btns, "confirm") && hasBtn(btns, "cancel") {
		snippet := text
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		i.Bus.Send(events.Event{
			Kind:    events.Warn,
			Message: fmt.Sprintf("MATCH-MISS: confirm+cancel buttons seen but isSellConfirmation returned false. full text: %q", snippet),
		})
	}

	// 2. "You caught a X" screen — sell or re-fish.
	if isCaughtFish(text) {
		return f.handleCaughtFish(ctx, i, m, text, btns)
	}

	// 3. 3x3 catch grid — analyze image, pick darkest cell.
	if isCatchGrid(btns) {
		return f.handleCatchGrid(i, m, btns)
	}

	// 4. Main /fish catch menu — click Go Fishing.
	if isMainFishMenu(text, btns) {
		f.mu.Lock()
		wantBuckets := f.wantBuckets
		f.wantBuckets = false
		f.mu.Unlock()
		if wantBuckets {
			if b := findBucketsButton(btns); b != nil {
				i.Bus.Send(events.Event{Kind: events.Info, Message: "opening a new bucket menu from the fishing menu"})
				f.mu.Lock()
				f.parentCatchMsgID = m.ID
				f.parentFishAgainCustom = goFishingCustom(btns)
				f.mu.Unlock()
				time.Sleep(humanDelay())
				return i.ClickButtonOnMessage(m, b.CustomID)
			}
			i.Bus.Send(events.Event{Kind: events.Warn, Message: "fishing menu has no Buckets button"})
		}
		for _, b := range btns {
			if strings.EqualFold(b.Label, "go fishing") {
				i.Bus.Send(events.Event{Kind: events.Activity, Activity: "fish"})
				time.Sleep(humanDelay())
				return i.ClickButton(m.ID, b.CustomID)
			}
		}
		return nil
	}

	// 5. Fallback — any enabled Fish Again button rescues us from miss
	// screens, result updates, and transitional states where no specific
	// handler matched. Without this the loop stalls after a wrong-click.
	for _, b := range btns {
		if strings.Contains(strings.ToLower(b.Label), "fish again") && !b.Disabled {
			time.Sleep(humanDelay())
			return i.ClickButton(m.ID, b.CustomID)
		}
	}

	return nil
}

func (f *Fish) handleCaughtFish(ctx context.Context, i *instance.Instance, m *discord.Message, text string, btns []*discord.MessageComponent) error {
	// Catch identity based on fish name + size. DM edits one parent message
	// across all catches, so m.ID alone can't tell catches apart — we need
	// a per-catch key to count fish correctly and to reset the sold flag.
	catchID := extractCatchID(text)
	f.mu.Lock()
	if len(f.seenCatches) > 200 {
		f.seenCatches = make(map[string]bool)
	}
	isNewCatch := catchID != "" && !f.seenCatches[catchID]
	if isNewCatch {
		f.seenCatches[catchID] = true
		// New catch on same message id → clear stale sold state.
		delete(f.soldMessages, m.ID)
	}
	alreadySold := f.soldMessages[m.ID]
	f.mu.Unlock()

	if isNewCatch {
		if match := fishCaughtRE.FindStringSubmatch(text); match != nil {
			name := strings.ToLower(strings.TrimSpace(match[1]))
			i.Bus.Send(events.Event{
				Kind:  events.FishCaught,
				Items: map[string]int{name: 1},
			})
		}
	}

	if n := extractFishTokens(text); n > 0 {
		i.Bus.Send(events.Event{Kind: events.FishToken, Reward: n})
	}

	sellMode := strings.ToLower(i.Cfg.Features.Fishing.SellMode)
	if sellMode == "" {
		sellMode = "fish_tokens"
	}

	sellBtns := []*discord.MessageComponent{}
	var fishAgainBtn *discord.MessageComponent
	var openBucketsBtn *discord.MessageComponent
	for idx, b := range btns {
		l := strings.ToLower(b.Label)
		if strings.Contains(l, "sell creature") || strings.Contains(l, "sell fish") {
			sellBtns = append(sellBtns, btns[idx])
		}
		if strings.Contains(l, "fish again") && !b.Disabled {
			fishAgainBtn = btns[idx]
		}
	}
	openBucketsBtn = findBucketsButton(btns)

	// Bucket-full overflow path: fish won't be kept in the per-fish sell,
	// so instead open/refresh the bucket viewer and sell everything in bulk.
	if !isBucketFull(text) {
		f.bucketNotFull()
	} else if f.bucketTryDue(time.Now()) {
		// Always update parent catch tracking (Fish Again custom_id, even if
		// currently disabled by cooldown — the id is stable).
		fishAgainCustom := ""
		for _, b := range btns {
			if strings.Contains(strings.ToLower(b.Label), "fish again") {
				fishAgainCustom = b.CustomID
				break
			}
		}
		f.mu.Lock()
		f.soldMessages[m.ID] = true
		f.parentCatchMsgID = m.ID
		f.parentFishAgainCustom = fishAgainCustom
		viewerID := f.bucketViewerMsgID
		refreshCustom := f.bucketViewerRefreshCustom
		f.mu.Unlock()

		step := f.nextBucketStep(time.Now(), openBucketsBtn != nil)

		f.mu.Lock()
		// Clear sold flag on existing bucket viewer so Sell All can fire
		// again after the refresh re-populates it.
		if viewerID != "" {
			delete(f.soldMessages, viewerID)
		}
		f.mu.Unlock()

		switch step {
		case bucketRestart:
			return f.restartFishing(i)
		case bucketRefresh:
			// Keeps the channel to one bucket viewer + one fishing message.
			i.Bus.Send(events.Event{Kind: events.Warn, Message: "bucket full - refreshing existing bucket viewer"})
			time.Sleep(humanDelay())
			err := i.ClickButton(viewerID, refreshCustom)
			f.mu.Lock()
			opened := f.openedViewer
			if err != nil {
				f.forgetViewerLocked()
				f.openedViewer = true
			}
			f.mu.Unlock()
			if err == nil || opened || openBucketsBtn == nil {
				return err
			}
			fallthrough
		case bucketOpen:
			// Its id + refresh button are captured in the viewer handler.
			i.Log.Info("bucket menu opened")
			i.Bus.Send(events.Event{Kind: events.Warn, Message: "bucket full - opening a new bucket menu"})
			time.Sleep(humanDelay())
			return i.ClickButtonOnMessage(m, openBucketsBtn.CustomID)
		}
	}

	if alreadySold {
		if fishAgainBtn != nil {
			time.Sleep(humanDelay())
			return i.ClickButtonOnMessage(m, fishAgainBtn.CustomID)
		}
		return nil
	}

	// Sell strategy branching. "bulk" (default): skip per-fish sell entirely,
	// just click Fish Again — bucket will fill after 10 catches and the
	// bucket-overflow path above kicks in to sell all at once. "per_fish":
	// click Sell Creature on every catch (the old behavior).
	strategy := strings.ToLower(i.Cfg.Features.Fishing.SellStrategy)
	if strategy == "" {
		strategy = "bulk"
	}

	if strategy == "per_fish" && len(sellBtns) > 0 {
		pick := pickSellButton(sellBtns, sellMode)
		time.Sleep(humanDelay())
		err := i.ClickButtonOnMessage(m, pick.CustomID)
		if err == nil {
			f.mu.Lock()
			f.soldMessages[m.ID] = true
			f.mu.Unlock()
		} else {
			i.Bus.Send(events.Event{Kind: events.Warn, Message: fmt.Sprintf("sell click failed, will retry on next update: %v", err)})
		}
		return err
	}

	// Bulk strategy OR per_fish but no sell button available → just fish again.
	if fishAgainBtn != nil {
		f.mu.Lock()
		f.soldMessages[m.ID] = true
		f.mu.Unlock()
		time.Sleep(humanDelay())
		return i.ClickButtonOnMessage(m, fishAgainBtn.CustomID)
	}

	return nil
}

// findBucketsButton finds the button that opens the Buckets menu: "Open
// Buckets" on the catch screen, "View Buckets" on the main fishing menu.
func findBucketsButton(btns []*discord.MessageComponent) *discord.MessageComponent {
	for _, b := range btns {
		if strings.Contains(strings.ToLower(b.Label), "bucket") && !b.Disabled {
			return b
		}
	}
	return nil
}

func goFishingCustom(btns []*discord.MessageComponent) string {
	for _, b := range btns {
		if strings.EqualFold(b.Label, "go fishing") {
			return b.CustomID
		}
	}
	return ""
}

// isBucketFull detects the "no more bucket space" warning on the caught-fish screen.
func isBucketFull(text string) bool {
	return strings.Contains(text, "no more bucket space") ||
		strings.Contains(text, "will not be kept")
}

// isBucketViewer detects the Buckets menu reached via "Open Buckets" button.
// Keyed on text + presence of the Sell All Fish button.
func isBucketViewer(text string, btns []*discord.MessageComponent) bool {
	if hasBtn(btns, "sell all fish") {
		return true
	}
	return strings.Contains(text, "viewing bucket slots") || strings.Contains(text, "all buckets value")
}

// findRefreshCustom returns the custom_id of the refresh button on the bucket
// viewer. The button has no text label (icon-only), so we identify it by
// elimination: the button that isn't Manage Slots / Sell All Fish / help ("?")
// and isn't a URL button is the refresh action.
func findRefreshCustom(btns []*discord.MessageComponent) string {
	known := map[string]bool{
		"manage slots":  true,
		"sell all fish": true,
		"?":             true,
	}
	for _, b := range btns {
		if b.URL != "" {
			continue
		}
		if b.CustomID == "" {
			continue
		}
		label := strings.ToLower(strings.TrimSpace(b.Label))
		if known[label] {
			continue
		}
		// Prefer emoji-only buttons (refresh is an icon-only button).
		if label == "" {
			return b.CustomID
		}
	}
	return ""
}

// isBucketSellConfirmation detects the "Selling Creatures" popup that appears
// after clicking Sell All Fish. Keyed only on presence of "Sell for" buttons —
// those labels are unique to this screen so no text match is needed. Text
// requirement was dropped because the popup text sometimes doesn't come through
// Components V2 the way we expected.
func isBucketSellConfirmation(text string, btns []*discord.MessageComponent) bool {
	for _, b := range btns {
		if strings.Contains(strings.ToLower(b.Label), "sell for") {
			return true
		}
	}
	return false
}

func (f *Fish) handleCatchGrid(i *instance.Instance, m *discord.Message, btns []*discord.MessageComponent) error {
	catchBtns := []*discord.MessageComponent{}
	for _, b := range btns {
		if strings.EqualFold(b.Label, "catch") {
			catchBtns = append(catchBtns, b)
		}
	}
	if len(catchBtns) == 0 {
		return nil
	}

	i.Bus.Send(events.Event{Kind: events.Activity, Activity: "fish"})

	imgURL := findCatchImageURL(m)
	pickIdx := -1

	if imgURL != "" {
		idx, err := analyzeFishImage(imgURL)
		if err != nil {
			i.Bus.Send(events.Event{
				Kind:    events.Warn,
				Message: fmt.Sprintf("fish image analysis failed: %v", err),
			})
		} else {
			pickIdx = idx
		}
	}

	if pickIdx < 0 || pickIdx >= len(catchBtns) {
		pickIdx = rand.Intn(len(catchBtns))
	}

	pick := catchBtns[pickIdx]
	time.Sleep(humanDelay())
	return i.ClickButton(m.ID, pick.CustomID)
}

// analyzeFishImage downloads the grid image, decodes it in-memory, and
// returns the darkest cell index (0..8). Does NOT write anything to disk —
// previous .debug/fish/ saves were for heuristic-tuning and are no longer
// needed now that pick-darkest is working.
func analyzeFishImage(url string) (int, error) {
	resp, err := http.Get(url)
	if err != nil {
		return -1, fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return -1, fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return -1, fmt.Errorf("read body: %w", err)
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return -1, fmt.Errorf("decode: %w", err)
	}

	// Rule confirmed by LO: exactly one fish per grid, any extras are mines.
	// Fish renders as the uniquely-darkest cell in the sample set.
	return darkestIndex(cellDarknessScores(img)), nil
}

// cellDarknessScores returns a per-cell (0..8) average darkness in [0,1],
// where 1.0 = pure black. Reads only the center of each cell to avoid edge
// artifacts (hex-tile boundaries in the water texture).
func cellDarknessScores(img image.Image) [9]float64 {
	var scores [9]float64
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	cellW := w / 3
	cellH := h / 3

	for cell := 0; cell < 9; cell++ {
		row := cell / 3
		col := cell % 3
		x0 := bounds.Min.X + col*cellW
		y0 := bounds.Min.Y + row*cellH
		scores[cell] = measureCellDarkness(img, x0, y0, cellW, cellH)
	}
	return scores
}

func darkestIndex(scores [9]float64) int {
	best := 0
	for i := 1; i < 9; i++ {
		if scores[i] > scores[best] {
			best = i
		}
	}
	return best
}

// topKDarkest is kept available for future tuning (random-from-top-N strategy)
// in case deterministic darkest-pick turns out to always hit the wrong feature.
func topKDarkest(scores [9]float64, k int) []int {
	indices := []int{0, 1, 2, 3, 4, 5, 6, 7, 8}
	sort.Slice(indices, func(a, b int) bool { return scores[indices[a]] > scores[indices[b]] })
	if k > 9 {
		k = 9
	}
	return indices[:k]
}

func measureCellDarkness(img image.Image, x0, y0, w, h int) float64 {
	insetX := w / 6
	insetY := h / 6
	totalDark := 0.0
	count := 0
	step := 2
	for y := y0 + insetY; y < y0+h-insetY; y += step {
		for x := x0 + insetX; x < x0+w-insetX; x += step {
			r, g, b, _ := img.At(x, y).RGBA()
			brightness := float64(r+g+b) / (3.0 * 65535.0)
			totalDark += 1.0 - brightness
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return totalDark / float64(count)
}

func formatScores(s [9]float64) string {
	parts := make([]string, 9)
	for i, v := range s {
		parts[i] = fmt.Sprintf("%.2f", v)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// Keep topKDarkest reachable even if unused in current heuristic.
var _ = topKDarkest

func pickSellButton(sellBtns []*discord.MessageComponent, mode string) *discord.MessageComponent {
	if len(sellBtns) == 1 {
		return sellBtns[0]
	}
	for _, b := range sellBtns {
		if b.Emoji == nil {
			continue
		}
		name := strings.ToLower(b.Emoji.Name)
		if mode == "fish_tokens" && (strings.Contains(name, "token") || strings.Contains(name, "fish")) {
			return b
		}
		if mode == "coins" && (strings.Contains(name, "coin") || strings.Contains(name, "gold")) {
			return b
		}
	}
	if mode == "fish_tokens" {
		return sellBtns[len(sellBtns)-1]
	}
	return sellBtns[0]
}

func isMainFishMenu(text string, btns []*discord.MessageComponent) bool {
	for _, b := range btns {
		if strings.EqualFold(b.Label, "go fishing") {
			return true
		}
	}
	return false
}

func isCatchGrid(btns []*discord.MessageComponent) bool {
	count := 0
	for _, b := range btns {
		if strings.EqualFold(b.Label, "catch") {
			count++
		}
	}
	return count >= 6
}

func isCaughtFish(text string) bool {
	return strings.Contains(text, "you caught")
}

func isSellConfirmation(text string, btns []*discord.MessageComponent) bool {
	// Only use strings verified from DM screenshots:
	//   - Title: "Pending Confirmation"
	//   - Button labels: "Confirm" and "Cancel"
	// Anything else is a guess — we ask LO for exact text before adding more.
	if !hasBtn(btns, "confirm") || !hasBtn(btns, "cancel") {
		return false
	}
	return strings.Contains(text, "pending confirmation")
}

// hasBtn reports whether btns contains an enabled button whose label equals name (case-insensitive).
func hasBtn(btns []*discord.MessageComponent, name string) bool {
	for _, b := range btns {
		if strings.EqualFold(b.Label, name) {
			return true
		}
	}
	return false
}

// findCatchImageURL walks all image sources: legacy attachments, legacy embed
// images, and Components V2 MediaGallery/Thumbnail (where DM actually puts
// the grid image in current builds).
func findCatchImageURL(m *discord.Message) string {
	for _, a := range m.Attachments {
		if a.URL != "" {
			return a.URL
		}
	}
	for _, e := range m.Embeds {
		if e.Image != nil && e.Image.URL != "" {
			return e.Image.URL
		}
	}
	urls := discord.CollectImageURLs(m.Components)
	if len(urls) > 0 {
		return urls[0]
	}
	return ""
}

func parseIntSafe(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not a digit: %c", c)
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}
