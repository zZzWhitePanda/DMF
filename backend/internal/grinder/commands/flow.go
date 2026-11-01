package commands

import (
	"context"
	"fmt"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/discord"
	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/events"
	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/instance"
)

// Flow drives Dank Memer's /flow command: one slash invocation, user picks
// which saved flow to use via TUI (first time), subsequent picker appearances
// auto-click the previously-chosen flow to recover from accidental stops.
//
// Cooldown is effectively "never re-invoke" (24h) because /flow is a single
// long-running session — same model as /fish catch. If the flow genuinely
// dies the watchdog nudges it back; a true dead-end routes through the
// stuck detector which re-fetches channel history.
type Flow struct {
	mu              sync.Mutex
	chosenFlowID    string // custom_id of the View button we clicked
	awaitingOutcome bool
	lastHint        int
	lastDecision    string

	// Watchdog state. lastUpdate ticks on every OnResponse call; the
	// watchdog goroutine (started once by Run) resyncs the channel if
	// lastUpdate stays stale for 20+ seconds — flow steps are quick, so
	// anything approaching that threshold means we missed an update.
	lastUpdate   time.Time
	lastMsgID    string
	watchdogOnce sync.Once
}

func (f *Flow) Name() string { return "flow" }

// 24-hour cooldown — effectively "run once per session". /flow is a long-
// running loop that drives itself via button clicks; the grinder's command
// picker should NEVER re-invoke /flow on its own because that spawns a
// second (orphaned) flow that confuses button targeting.
func (f *Flow) DefaultCooldown() time.Duration { return 24 * time.Hour }

func (f *Flow) Run(ctx context.Context, i *instance.Instance) (time.Duration, error) {
	// Reset only the highlow outcome flag. chosenFlowID is intentionally
	// preserved so that if /flow re-invokes mid-session the picker can
	// auto-click the previously-chosen View without prompting the user again.
	f.mu.Lock()
	f.awaitingOutcome = false
	f.lastUpdate = time.Now()
	f.mu.Unlock()

	// Spawn the stuck-watchdog once per instance lifetime.
	f.watchdogOnce.Do(func() {
		go f.runWatchdog(ctx, i)
	})

	if err := i.InvokeSlashCommand("flow", nil); err != nil {
		return 60 * time.Second, err
	}
	return 24 * time.Hour, nil
}

// runWatchdog nudges a stuck /flow session back to life. Two escalating
// strategies:
//
//  1. STANDARD NUDGE: re-dispatch recent channel history via Instance.Resync.
//     Covers the common case of a missed MESSAGE_UPDATE — the resync
//     re-fires onMessage on recent DMs and the normal OnResponse chain
//     clicks whatever's current.
//
//  2. FORCE-CLICK ESCALATION: after 3 consecutive stuck nudges with no
//     progress, bypass OnResponse entirely. Fetch the last 5 DM
//     messages directly, scan for ANY enabled Continue / Next / Start
//     / Accept / Proceed button, and click it with full message_flags
//     passed through. This handles the case where OnResponse keeps
//     deciding the state isn't actionable even though a human sitting
//     at the keyboard would just click Continue and move on. Which is
//     exactly LO's recurring complaint: "all it needs to do is find
//     the latest /flow and click continue."
//
// Checked every 5 seconds. Honors pause (user pause or humanized break)
// so the stuck clock doesn't tick while the grinder is deliberately
// idle.
func (f *Flow) runWatchdog(ctx context.Context, i *instance.Instance) {
	const (
		checkEvery        = 5 * time.Second
		stuckAfter        = 20 * time.Second
		cooldownGap       = 20 * time.Second
		escalateAfter     = 3 // consecutive nudges with no progress
	)
	ticker := time.NewTicker(checkEvery)
	defer ticker.Stop()
	var lastNudge time.Time
	consecutiveStuck := 0
	var lastNudgeUpdateMark time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if i.State.IsPaused() {
			f.mu.Lock()
			f.lastUpdate = time.Now()
			f.mu.Unlock()
			// Reset escalation when we go into pause — fresh state on resume.
			consecutiveStuck = 0
			continue
		}
		f.mu.Lock()
		idle := time.Since(f.lastUpdate)
		msgID := f.lastMsgID
		lastUpdate := f.lastUpdate
		f.mu.Unlock()
		if idle < stuckAfter {
			// Progress detected since last nudge — reset escalation counter.
			if !lastNudgeUpdateMark.IsZero() && lastUpdate.After(lastNudgeUpdateMark) {
				consecutiveStuck = 0
			}
			continue
		}
		if msgID == "" {
			continue
		}
		if time.Since(lastNudge) < cooldownGap {
			continue
		}
		lastNudge = time.Now()
		consecutiveStuck++

		if consecutiveStuck < escalateAfter {
			// Standard resync path.
			i.Bus.Send(events.Event{
				Kind:    events.Warn,
				Message: fmt.Sprintf("flow idle %v - nudging via channel resync (attempt %d/%d before force-click)", idle.Round(time.Second), consecutiveStuck, escalateAfter),
			})
			i.Resync(ctx)
		} else {
			// Escalate: direct force-click on the latest DM message's
			// Continue (or equivalent) button, bypassing OnResponse.
			i.Bus.Send(events.Event{
				Kind:    events.Warn,
				Message: fmt.Sprintf("flow stuck %v across %d resyncs — escalating to direct force-click on latest DM msg", idle.Round(time.Second), consecutiveStuck),
			})
			f.forceClickLatest(ctx, i)
			consecutiveStuck = 0
		}

		// Mark the lastUpdate value AT nudge time; next tick compares to
		// detect whether anything progressed (OnResponse would push
		// lastUpdate forward past this mark).
		f.mu.Lock()
		f.lastUpdate = time.Now()
		f.mu.Unlock()
		lastNudgeUpdateMark = time.Now()
	}
}

// forceClickLatest fetches recent channel history, finds OUR specific
// /flow invocation's current message (DM edits one message across every
// flow step, so m.Interaction pins it to the original slash command),
// and clicks the first enabled advance-style button it carries. Called
// by the watchdog's escalation path after N standard resyncs fail.
//
// Why target by Interaction.Name instead of just "newest DM message":
// random events, sell confirmations, hunt/beg responses, and any other
// DM traffic can land AFTER the flow step is drawn. If we picked by
// recency, we'd click Continue on a Dank Memer Corp event instead of
// the actual flow. Walks up to 20 messages so a long gap of other DM
// chatter above the flow message doesn't hide it.
//
// Uses ClickButtonOnMessage so ephemeral flow steps work (message_flags
// passthrough).
func (f *Flow) forceClickLatest(ctx context.Context, i *instance.Instance) {
	msgs, err := i.Rest.GetChannelMessages(i.ChannelID, 20)
	if err != nil {
		i.Bus.Send(events.Event{Kind: events.Warn, Message: fmt.Sprintf("force-click fetch failed: %v", err)})
		return
	}
	wanted := []string{"continue", "next", "start", "accept", "proceed", "roll"}
	for _, m := range msgs {
		if ctx.Err() != nil {
			return
		}
		if m.Author.ID != discord.DankMemerAppID && m.ApplicationID != discord.DankMemerAppID {
			continue
		}
		if !isOurFlowMessage(&m, i.SelfUserID) {
			continue
		}
		btns := discord.CollectButtons(m.Components)
		for _, kw := range wanted {
			for _, b := range btns {
				if b.Disabled || b.CustomID == "" {
					continue
				}
				if strings.Contains(strings.ToLower(b.Label), kw) {
					i.Bus.Send(events.Event{
						Kind:    events.Info,
						Message: fmt.Sprintf("force-click: pressing %q on flow message %s", b.Label, m.ID),
					})
					msgCopy := m
					if err := i.ClickButtonOnMessage(&msgCopy, b.CustomID); err != nil {
						i.Bus.Send(events.Event{Kind: events.Warn, Message: fmt.Sprintf("force-click failed: %v", err)})
					}
					f.mu.Lock()
					f.lastUpdate = time.Now()
					f.lastMsgID = m.ID
					f.mu.Unlock()
					return
				}
			}
		}
		// Found the flow message but no clickable advance button — stop
		// looking (there's only one live flow message per session).
		i.Bus.Send(events.Event{Kind: events.Warn, Message: fmt.Sprintf("force-click: found flow message %s but no enabled Continue/Next/Start/Accept button present", m.ID)})
		return
	}
	i.Bus.Send(events.Event{Kind: events.Warn, Message: "force-click: couldn't locate our /flow message in last 20 channel messages (Interaction field missing or stale)"})
}

// isOurFlowMessage is a thin wrapper around the shared command-message
// identifier, kept so the force-click + watchdog call sites read
// clearly ("is this a flow message?" vs the more generic helper).
func isOurFlowMessage(m *discord.Message, selfUserID string) bool {
	return isOurCommandMessage(m, "flow", selfUserID)
}

// Multiple hint patterns, tried in order of specificity.
var hintPatterns = []*regexp.Regexp{
	regexp.MustCompile(`than\s+\*{0,2}(\d{1,3})\*{0,2}`),
	regexp.MustCompile(`number\s+is\s+\*{0,2}(\d{1,3})\*{0,2}`),
	regexp.MustCompile(`hint[:\s]+\*{0,2}(\d{1,3})\*{0,2}`),
	regexp.MustCompile(`current\s+number[:\s]+\*{0,2}(\d{1,3})\*{0,2}`),
	// Loose fallback: any bold-wrapped 1-99 number in a highlow context.
	regexp.MustCompile(`\*\*(\d{1,2})\*\*`),
}

func parseHint(text string) int {
	for _, re := range hintPatterns {
		m := re.FindStringSubmatch(text)
		if m == nil {
			continue
		}
		h, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		if h >= 1 && h <= 99 {
			return h
		}
	}
	return -1
}

func (f *Flow) OnResponse(ctx context.Context, i *instance.Instance, m *discord.Message) error {
	// Gate: only process messages that belong to our own /flow
	// invocation chain. The main instance loop sets Flow as pendingCmd,
	// which routes EVERY DM message in the channel to this handler —
	// without the gate, running /balance while the bot is grinding
	// would see Flow's fallback random-button-click fire on the
	// /balance response and press Withdraw or Deposit. The gate also
	// filters out other users' /flow sessions, random event messages,
	// sell confirmations, hunt/beg responses, and any other DM traffic.
	if !isOurFlowMessage(m, i.SelfUserID) {
		return nil
	}

	text := buildText(m)
	btns := discord.CollectButtons(m.Components)
	sel := discord.FindSelect(m.Components)

	// Watchdog clock: every OnResponse call here is now guaranteed to
	// be our live flow message (gated above), so an unconditional tick
	// is safe. Random events + unrelated DM traffic can't reach this
	// line anymore.
	f.mu.Lock()
	f.lastUpdate = time.Now()
	f.lastMsgID = m.ID
	f.mu.Unlock()

	// First priority: did we just make a highlow decision? Check outcome.
	f.mu.Lock()
	if f.awaitingOutcome {
		if won, had := parseHighlowOutcome(text); had {
			f.awaitingOutcome = false
			outcome := "lost"
			if won {
				outcome = "won"
			}
			i.Bus.Send(events.Event{
				Kind:    events.HighlowOutcome,
				Command: "flow",
				Outcome: outcome,
				Message: f.lastDecision,
			})
		}
	}
	chosenID := f.chosenFlowID
	needsPicker := isFlowPicker(m, btns)
	f.mu.Unlock()

	// Flow picker. First time: prompt the user. Subsequent appearances
	// (e.g. accidental Stop → DM showed the picker again): auto-click the
	// same View button so the flow resumes without manual intervention.
	if needsPicker {
		viewBtns := extractViewButtons(btns)
		if len(viewBtns) == 0 {
			return nil
		}

		if chosenID != "" {
			// Auto-click the previous choice.
			for _, b := range viewBtns {
				if b.CustomID == chosenID {
					i.Bus.Send(events.Event{
						Kind:    events.Info,
						Message: "flow picker shown again, auto-clicking previous choice",
					})
					time.Sleep(humanDelay())
					return i.ClickButtonOnMessage(m, b.CustomID)
				}
			}
			// Button custom_id changed; fall back to the first View.
			i.Bus.Send(events.Event{
				Kind:    events.Info,
				Message: "flow picker shown; previous button missing, clicking first View",
			})
			f.mu.Lock()
			f.chosenFlowID = viewBtns[0].CustomID
			f.mu.Unlock()
			time.Sleep(humanDelay())
			return i.ClickButtonOnMessage(m, viewBtns[0].CustomID)
		}

		// First time: ask the user via TUI picker.
		labels := make([]string, 0, len(viewBtns))
		for _, b := range viewBtns {
			labels = append(labels, b.Label)
		}
		i.Bus.Send(events.Event{
			Kind:    events.FlowPickerNeeded,
			Message: extractContext(m),
			Choices: labels,
		})
		idx, err := i.Bus.AwaitFlowChoice(ctx)
		if err != nil {
			return err
		}
		if idx < 0 || idx >= len(viewBtns) {
			return nil
		}
		f.mu.Lock()
		f.chosenFlowID = viewBtns[idx].CustomID
		f.mu.Unlock()
		time.Sleep(humanDelay())
		return i.ClickButtonOnMessage(m, viewBtns[idx].CustomID)
	}

	// Emit current activity if we can tell what sub-command is running.
	if act := detectActivity(text); act != "" {
		i.Bus.Send(events.Event{Kind: events.Activity, Activity: act})
	}

	// Highlow decision prompt.
	if isHighlowPrompt(text, btns) {
		return f.clickHighlow(i, m, text, btns)
	}

	// Select menu (postmemes-style platform picker).
	if sel != nil && len(sel.Options) > 0 {
		pick := sel.Options[rand.Intn(len(sel.Options))]
		time.Sleep(humanDelay())
		return i.SelectMenu(m.ID, sel.CustomID, []string{pick.Value})
	}

	if len(btns) == 0 {
		return nil
	}

	// Choice-screen detection. Flow steps like "What crime do you want to
	// commit?" lay out the actual options AND a Continue + Skip pair. The
	// previous priority order clicked Continue first, which either stalls
	// the flow or forfeits the step (click Continue with nothing selected
	// → DM waits forever). Rule: if BOTH Continue and Skip are present,
	// everything else is a choice — pick a random one (per LO: any option
	// is fine on crime screens, same model applies to other picker steps).
	// After we click an option DM updates the message; the next OnResponse
	// re-enters this function with the updated button set (options gone,
	// Continue remains) and the "prefer continue" branch fires correctly.
	hasContinue := false
	hasSkip := false
	for _, b := range btns {
		l := strings.ToLower(b.Label)
		if strings.Contains(l, "continue") {
			hasContinue = true
		}
		if strings.Contains(l, "skip") {
			hasSkip = true
		}
	}
	if hasContinue && hasSkip {
		options := []*discord.MessageComponent{}
		for _, b := range btns {
			l := strings.ToLower(b.Label)
			if strings.Contains(l, "continue") || strings.Contains(l, "skip") {
				continue
			}
			if isDestructiveLabel(b.Label) {
				continue
			}
			if b.Disabled {
				continue
			}
			options = append(options, b)
		}
		if len(options) > 0 {
			pick := options[rand.Intn(len(options))]
			i.Bus.Send(events.Event{
				Kind:    events.Info,
				Message: fmt.Sprintf("flow choice screen: picking %q", pick.Label),
			})
			time.Sleep(humanDelay())
			return i.ClickButtonOnMessage(m, pick.CustomID)
		}
		// No options alongside Continue/Skip — fall through to the
		// Continue preference below (nothing to pick, just advance).
	}

	// Prefer explicit continue/next buttons.
	for _, b := range btns {
		l := strings.ToLower(b.Label)
		if strings.Contains(l, "continue") ||
			strings.Contains(l, "next") ||
			strings.Contains(l, "start") ||
			strings.Contains(l, "accept") ||
			strings.Contains(l, "roll") ||
			strings.Contains(l, "proceed") {
			time.Sleep(humanDelay())
			return i.ClickButtonOnMessage(m, b.CustomID)
		}
	}

	// Fallback: random enabled button, but filter out destructive buttons
	// (Stop/Cancel/End/Import/Create New/etc.) so the grinder doesn't
	// accidentally end the flow when only destructive buttons remain.
	safe := []*discord.MessageComponent{}
	for _, b := range btns {
		if !isDestructiveLabel(b.Label) {
			safe = append(safe, b)
		}
	}
	if len(safe) == 0 {
		// Only destructive options available — refuse to click.
		return nil
	}
	pick := safe[rand.Intn(len(safe))]
	time.Sleep(humanDelay())
	return i.ClickButtonOnMessage(m, pick.CustomID)
}

// isDestructiveLabel returns true for buttons whose click would end or
// disrupt the flow (Stop/Cancel/Delete/etc.) or that we never want to
// randomly click from the picker screen (Import/Create New).
func isDestructiveLabel(label string) bool {
	l := strings.ToLower(label)
	for _, bad := range []string{
		"stop", "cancel", "end", "quit", "exit",
		"delete", "disable", "import", "create new",
		"abandon", "leave",
	} {
		if strings.Contains(l, bad) {
			return true
		}
	}
	return false
}

func (f *Flow) clickHighlow(i *instance.Instance, m *discord.Message, text string, btns []*discord.MessageComponent) error {
	hint := parseHint(text)

	want := ""
	switch {
	case hint == -1:
		want = ""
	case hint < 45:
		want = "higher"
	case hint > 55:
		want = "lower"
	default:
		want = "jackpot"
	}

	f.mu.Lock()
	f.lastHint = hint
	f.lastDecision = want
	f.awaitingOutcome = true
	f.mu.Unlock()

	i.Bus.Send(events.Event{
		Kind:    events.HighlowDecision,
		Command: "flow",
		Message: fmt.Sprintf("hint=%d chose=%s", hint, want),
	})

	if want != "" {
		for _, b := range btns {
			if strings.Contains(strings.ToLower(b.Label), want) {
				time.Sleep(humanDelay())
				return i.ClickButtonOnMessage(m, b.CustomID)
			}
		}
	}
	if len(btns) == 0 {
		return nil
	}
	pick := btns[rand.Intn(len(btns))]
	time.Sleep(humanDelay())
	return i.ClickButtonOnMessage(m, pick.CustomID)
}

func isHighlowPrompt(text string, btns []*discord.MessageComponent) bool {
	if strings.Contains(text, "higher or lower") || strings.Contains(text, "high or low") {
		return true
	}
	hits := 0
	for _, b := range btns {
		l := strings.ToLower(b.Label)
		if strings.Contains(l, "higher") || strings.Contains(l, "lower") || strings.Contains(l, "jackpot") {
			hits++
		}
	}
	return hits >= 2
}

func isFlowPicker(m *discord.Message, btns []*discord.MessageComponent) bool {
	viewCount := 0
	for _, b := range btns {
		if strings.Contains(strings.ToLower(b.Label), "view") {
			viewCount++
		}
	}
	if viewCount == 0 {
		return false
	}
	for _, e := range m.Embeds {
		lt := strings.ToLower(e.Title + " " + e.Description)
		if strings.Contains(lt, "flow") || strings.Contains(lt, "which") || strings.Contains(lt, "choose") {
			return true
		}
	}
	// Multiple view buttons strongly implies picker.
	return viewCount >= 2
}

func extractViewButtons(btns []*discord.MessageComponent) []*discord.MessageComponent {
	out := []*discord.MessageComponent{}
	for _, b := range btns {
		if strings.Contains(strings.ToLower(b.Label), "view") {
			out = append(out, b)
		}
	}
	return out
}

func extractContext(m *discord.Message) string {
	var sb strings.Builder
	for _, e := range m.Embeds {
		if e.Title != "" {
			sb.WriteString(e.Title + "\n")
		}
		if e.Description != "" {
			sb.WriteString(e.Description + "\n")
		}
		for _, fld := range e.Fields {
			sb.WriteString(fld.Name + ": " + fld.Value + "\n")
		}
	}
	return strings.TrimSpace(sb.String())
}

// detectActivity maps DM phrasing to a short activity label.
// Includes past-tense verbs since the flow headers use "X searched the twitter" / "X dug up Y".
func detectActivity(text string) string {
	checks := []struct{ kw, name string }{
		{"higher or lower", "highlow"},
		{"high or low", "highlow"},
		// dig
		{"digging", "dig"},
		{"dug up", "dig"},
		{"dug", "dig"},
		{"dig site", "dig"},
		{"your shovel", "dig"},
		// hunt
		{"hunting", "hunt"},
		{"hunted", "hunt"},
		{"safari", "hunt"},
		{"your rifle", "hunt"},
		// beg
		{"begging", "beg"},
		{"begged", "beg"},
		{"beg for money", "beg"},
		{"panhandle", "beg"},
		// search
		{"searched the", "search"},
		{"searching", "search"},
		{"search for", "search"},
		{"places to search", "search"},
		// tidy
		{"tidepod", "tidy"},
		{"tidying", "tidy"},
		{"tidied", "tidy"},
		// crime
		{"commit crime", "crime"},
		{"committed", "crime"},
		{"criminal", "crime"},
		{"crime", "crime"},
		// postmeme
		{"posted a meme", "postmeme"},
		{"posted on", "postmeme"},
		{"posting", "postmeme"},
		{"post meme", "postmeme"},
		{"postmeme", "postmeme"},
		{"social media", "postmeme"},
	}
	for _, c := range checks {
		if strings.Contains(text, c.kw) {
			return c.name
		}
	}
	return ""
}

func parseHighlowOutcome(text string) (won, had bool) {
	if strings.Contains(text, "correct") || strings.Contains(text, "you won") || strings.Contains(text, "jackpot") {
		return true, true
	}
	if strings.Contains(text, "wrong") || strings.Contains(text, "you lost") ||
		strings.Contains(text, "better luck") || strings.Contains(text, "incorrect") {
		return false, true
	}
	return false, false
}

func buildText(m *discord.Message) string {
	t := strings.ToLower(m.Content)
	for _, e := range m.Embeds {
		t += " " + strings.ToLower(e.Title) + " " + strings.ToLower(e.Description)
		for _, fld := range e.Fields {
			t += " " + strings.ToLower(fld.Name) + " " + strings.ToLower(fld.Value)
		}
	}
	// Components V2: pull TextDisplay content.
	for _, s := range discord.CollectText(m.Components) {
		t += " " + strings.ToLower(s)
	}
	return t
}

func humanDelay() time.Duration {
	return time.Duration(500+rand.Intn(2000)) * time.Millisecond
}
