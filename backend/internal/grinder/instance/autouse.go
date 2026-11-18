package instance

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/config"
	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/discord"
	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/events"
	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/humanize"
)

const (
	// How long to wait for Dank Memer to answer a /use.
	useReplyTimeout = 12 * time.Second
	// When to try again after a /use that didn't clearly work. Never later
	// than the item's own gap.
	useRetryAfter = 2 * time.Minute
)

// autoUser keeps a list of items in use: each is used once before the first
// command, then again whenever its gap comes round. An item stops when it hits
// its limit, runs out or isn't recognised, and waits for Continue.
type autoUser struct {
	mu    sync.Mutex
	slots []*autoUseSlot

	// Dank Memer's replies to our /use commands. Buffered and never blocked
	// on: a reply nobody is waiting for is just dropped.
	replies chan *discord.Message
	// The highest message ID among those replies. Dank Memer edits old replies
	// later on, and an edit keeps its old ID, so anything at or below this when
	// a /use goes out belongs to an earlier one.
	newest uint64
}

type autoUseSlot struct {
	name   string
	every  time.Duration
	limit  int
	used   int
	left   int // -1 until Dank Memer says
	nextAt time.Time
	held   string // why it's stopped, "" while in use
}

func newAutoUser(cfg config.AutoUseConfig, now time.Time) *autoUser {
	if !cfg.Enabled || len(cfg.Items) == 0 {
		return nil
	}
	a := &autoUser{replies: make(chan *discord.Message, 32)}
	for _, it := range cfg.Items {
		// Due straight away, so everything is used before the first command.
		a.slots = append(a.slots, &autoUseSlot{name: it.Name, every: it.Every.Std(), limit: it.Limit, left: -1, nextAt: now})
	}
	return a
}

// due returns the first item that should be used now, or nil.
func (a *autoUser) due(now time.Time) *autoUseSlot {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, s := range a.slots {
		if s.held == "" && !now.Before(s.nextAt) {
			return s
		}
	}
	return nil
}

// status is the list as the dashboard sees it. A new slice every call.
func (a *autoUser) status() []events.AutoUseStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]events.AutoUseStatus, 0, len(a.slots))
	for _, s := range a.slots {
		st := events.AutoUseStatus{
			Name:  s.name,
			Every: int64(s.every / time.Second),
			Used:  s.used,
			Limit: s.limit,
			Left:  s.left,
			Held:  s.held,
		}
		if s.held == "" {
			st.NextAt = s.nextAt.Unix()
		}
		out = append(out, st)
	}
	return out
}

// resume starts using a held item again with a fresh count. Reports false if
// the item isn't held.
func (a *autoUser) resume(name string, now time.Time) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, s := range a.slots {
		if strings.EqualFold(s.name, name) && s.held != "" {
			s.held = ""
			s.used = 0
			s.nextAt = now
			return s.name, true
		}
	}
	return "", false
}

// record applies Dank Memer's answer to a use of s, and returns s as it now
// stands. Anything unclear leaves s alone, for the caller to retry.
func (a *autoUser) record(s *autoUseSlot, outcome useOutcome, left int, now time.Time) autoUseSlot {
	a.mu.Lock()
	defer a.mu.Unlock()
	if left >= 0 {
		s.left = left
	}
	switch outcome {
	case useOK:
		s.used++
		s.nextAt = now.Add(s.every)
		switch {
		case s.left == 0:
			s.held = events.HeldOut // that was the last one
		case s.limit > 0 && s.used >= s.limit:
			s.held = events.HeldLimit
		}
	case useOut:
		s.held = events.HeldOut
	case useUnknown:
		s.held = events.HeldUnknown
	}
	return *s
}

// offer passes along a reply to one of our /use commands.
func (a *autoUser) offer(m *discord.Message) {
	a.mu.Lock()
	a.newest = max(a.newest, snowflake(m.ID))
	a.mu.Unlock()
	select {
	case a.replies <- m:
	default:
	}
}

// replyFloor is the newest reply seen so far. Replies to a /use sent now
// will have higher IDs.
func (a *autoUser) replyFloor() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.newest
}

// snowflake reads a Discord ID, which grows over time. 0 if it isn't one.
func snowflake(id string) uint64 {
	n, _ := strconv.ParseUint(id, 10, 64)
	return n
}

func (a *autoUser) drainReplies() {
	for {
		select {
		case <-a.replies:
		default:
			return
		}
	}
}

// useOutcome is what Dank Memer said to a /use.
type useOutcome int

const (
	useUnclear  useOutcome = iota // nothing we recognise, or no reply yet
	useOK                         // the item was used
	useOut                        // none left to use
	useUnknown                    // no item by that name
	useNotReady                   // still active or on cooldown, so not used
)

var (
	// "You have 228 Lucky Horseshoe left", a disabled button under a use.
	useLeftRE = regexp.MustCompile(`(?i)you have ([\d,]+)\s.*\bleft\b`)
	// "You have no Lucky Horseshoe left" after using the last one.
	useNoneLeftRE = regexp.MustCompile(`(?i)you have no\s.*\bleft\b`)
)

// classifyUse reads Dank Memer's reply to /use. left is how many it says
// remain, or -1 if it doesn't say.
func classifyUse(m *discord.Message) (useOutcome, int) {
	texts := useReplyTexts(m)
	left := -1
	for _, t := range texts {
		if match := useLeftRE.FindStringSubmatch(t); match != nil {
			if n, err := strconv.Atoi(strings.ReplaceAll(match[1], ",", "")); err == nil {
				left = n
			}
		} else if useNoneLeftRE.MatchString(t) {
			left = 0
		}
	}
	all := strings.ToLower(strings.Join(texts, "\n"))
	all = strings.ReplaceAll(all, "’", "'")

	// Failures first: their wording overlaps with success ("you can't use").
	switch {
	case containsAny(all, useUnknownPhrases):
		return useUnknown, left
	case containsAny(all, useOutPhrases):
		return useOut, 0
	// The "N left" button only comes with a use that went through.
	case left >= 0:
		return useOK, left
	case containsAny(all, useNotReadyPhrases):
		return useNotReady, left
	case containsAny(all, useOKPhrases):
		return useOK, left
	}
	return useUnclear, left
}

// Lowercase phrases classifyUse looks for, apostrophes straightened.
var (
	useUnknownPhrases = []string{"couldn't find", "could not find", "not a valid", "isn't a valid", "not a real item",
		"no item", "not an item", "isn't an item", "unknown item", "doesn't exist", "does not exist"}
	useOutPhrases = []string{"don't own", "do not own", "dont own", "don't have any", "do not have any",
		"dont have any", "don't have this", "don't have that", "you own 0"}
	useNotReadyPhrases = []string{"already", "still active", "cooldown", "slow down", "slow it down",
		"wait ", "try again"}
	useOKPhrases = []string{"you equip", "you used", "you use your", "you use a", "you use an", "you ate",
		"you drank", "you drink", "you eat", "wear off", "wears off", "activated", "you open"}
)

// useReplyTexts is every bit of text in a reply, button labels included:
// the "You have N left" line is a disabled button.
func useReplyTexts(m *discord.Message) []string {
	out := []string{m.Content}
	for _, e := range m.Embeds {
		out = append(out, e.Title, e.Description)
		for _, f := range e.Fields {
			out = append(out, f.Name, f.Value)
		}
	}
	out = append(out, discord.CollectText(m.Components)...)
	var walk func([]discord.MessageComponent)
	walk = func(cs []discord.MessageComponent) {
		for i := range cs {
			if cs[i].Label != "" {
				out = append(out, cs[i].Label)
			}
			if a := cs[i].Accessory; a != nil && a.Label != "" {
				out = append(out, a.Label)
			}
			walk(cs[i].Components)
		}
	}
	walk(m.Components)
	return out
}

// useDueItem uses the next item that's due, if any, and reports whether it
// sent anything. The main loop calls it between commands, so item use never
// overlaps a command and stops for pauses, breaks and shifts.
func (i *Instance) useDueItem(ctx context.Context) bool {
	a := i.autoUse
	if a == nil {
		return false
	}
	slot := a.due(time.Now())
	if slot == nil {
		return false
	}

	humanize.SleepRand(i.Cfg.Humanize.MinDelay.Std(), i.Cfg.Humanize.MaxDelay.Std())
	if ctx.Err() != nil || i.State.IsPaused() {
		return true
	}
	if i.Cfg.Humanize.TypingIndicator {
		_ = i.Rest.SendTyping(i.ChannelID)
		humanize.SleepRand(500*time.Millisecond, i.Cfg.Humanize.TypingVariance.Std())
	}

	outcome, left, reply, err := i.sendUse(ctx, slot.name)
	switch {
	case ctx.Err() != nil:
	case err != nil:
		i.retryItem(slot, fmt.Sprintf("couldn't send /use %s: %v", slot.name, err))
	default:
		i.reportUse(slot, outcome, left, reply)
	}
	return true
}

// sendUse runs /use item:<name> and reads Dank Memer's reply.
func (i *Instance) sendUse(ctx context.Context, name string) (useOutcome, int, *discord.Message, error) {
	a := i.autoUse
	a.drainReplies()
	floor := a.replyFloor()
	// No Activity event: nothing would clear it afterwards in classic mode,
	// so the status line would say "Running /use" long after. The feed
	// gets a line for every use instead.
	if err := i.InvokeSlashCommand("use", []map[string]interface{}{
		{"type": 3, "name": "item", "value": name},
	}); err != nil {
		return useUnclear, -1, nil, err
	}
	outcome, left, reply := i.awaitUseReply(ctx, floor, useReplyTimeout)
	return outcome, left, reply, nil
}

// reportUse records what Dank Memer said to a use of slot, and tells the
// dashboard.
func (i *Instance) reportUse(slot *autoUseSlot, outcome useOutcome, left int, reply *discord.Message) {
	a := i.autoUse
	st := a.record(slot, outcome, left, time.Now())
	switch outcome {
	case useOK:
		i.Bus.Send(events.Event{
			Kind:    events.ItemUsed,
			Command: st.name,
			Reward:  st.used,
			Message: useProgress(st.used, st.limit, st.left, st.every, st.held),
			AutoUse: a.status(),
		})
		if st.held != "" {
			i.holdItem(st.name, st.held, st.limit)
		}
	case useOut, useUnknown:
		i.holdItem(st.name, st.held, st.limit)
	case useNotReady:
		i.retryItem(slot, fmt.Sprintf("%s isn't ready to use again yet: %s", slot.name, replySnippet(reply)))
	default:
		if reply == nil {
			i.retryItem(slot, fmt.Sprintf("no reply from Dank Memer to /use %s", slot.name))
		} else {
			i.retryItem(slot, fmt.Sprintf("couldn't tell if %s was used, Dank Memer said: %s", slot.name, replySnippet(reply)))
		}
	}
}

// holdItem tells the dashboard an item has stopped until Continue is pressed.
func (i *Instance) holdItem(name, why string, limit int) {
	var msg string
	switch why {
	case events.HeldLimit:
		msg = fmt.Sprintf("%s hit its limit of %d, waiting for you to press Continue", name, limit)
	case events.HeldOut:
		msg = fmt.Sprintf("out of %s, waiting for you to press Continue", name)
	default:
		msg = fmt.Sprintf("Dank Memer doesn't know an item called %q, check the name in Settings", name)
	}
	i.Bus.Send(events.Event{Kind: events.ItemHeld, Command: name, Outcome: why, Message: msg, AutoUse: i.autoUse.status()})
}

// retryItem tries an item again soon without counting it as used.
func (i *Instance) retryItem(slot *autoUseSlot, why string) {
	a := i.autoUse
	a.mu.Lock()
	slot.nextAt = time.Now().Add(min(useRetryAfter, slot.every))
	wait := humanize.FormatDuration(time.Until(slot.nextAt))
	a.mu.Unlock()
	i.Bus.Send(events.Event{Kind: events.Warn, Message: why + ", trying again in " + wait, AutoUse: a.status()})
}

// awaitUseReply waits for Dank Memer to answer the /use just sent, ignoring
// replies with IDs at or below floor. Dank Memer can post a "thinking"
// placeholder before editing in the real reply, so this keeps reading until
// something is recognised or time runs out.
func (i *Instance) awaitUseReply(ctx context.Context, floor uint64, wait time.Duration) (useOutcome, int, *discord.Message) {
	timeout := time.NewTimer(wait)
	defer timeout.Stop()
	var last *discord.Message
	left := -1
	for {
		select {
		case <-ctx.Done():
			return useUnclear, left, last
		case <-timeout.C:
			return useUnclear, left, last
		case m := <-i.autoUse.replies:
			if id := snowflake(m.ID); id != 0 && id <= floor {
				continue
			}
			outcome, l := classifyUse(m)
			if l >= 0 {
				left = l
			}
			if strings.TrimSpace(strings.Join(useReplyTexts(m), "")) != "" {
				last = m
			}
			if outcome != useUnclear {
				return outcome, left, m
			}
		}
	}
}

// listenForContinue starts held items again when the dashboard says so.
func (i *Instance) listenForContinue(ctx context.Context) {
	if i.autoUse == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case name := <-i.Bus.AutoUseContinues():
			if got, ok := i.autoUse.resume(name, time.Now()); ok {
				i.Bus.Send(events.Event{Kind: events.Info, Message: "using " + got + " again", AutoUse: i.autoUse.status()})
			}
		}
	}
}

// announceAutoUse puts the item list on the dashboard before anything is used.
func (i *Instance) announceAutoUse() {
	if i.autoUse == nil {
		return
	}
	st := i.autoUse.status()
	names := make([]string, len(st))
	for n, s := range st {
		names[n] = s.Name
	}
	i.Bus.Send(events.Event{Kind: events.Info, Message: "auto-use on: " + strings.Join(names, ", "), AutoUse: st})
}

// useProgress is the feed's detail for a use: "3 of 10 · 225 left · next in 15m 0s".
func useProgress(used, limit, left int, every time.Duration, held string) string {
	parts := []string{}
	if limit > 0 {
		parts = append(parts, fmt.Sprintf("%d of %d", used, limit))
	} else {
		parts = append(parts, fmt.Sprintf("%d used", used))
	}
	if left >= 0 {
		parts = append(parts, fmt.Sprintf("%s left", commas(left)))
	}
	if held == "" {
		parts = append(parts, "next in "+humanize.FormatDuration(every))
	}
	return strings.Join(parts, " · ")
}

func commas(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// replySnippet is a short one-line quote of a reply for the feed.
func replySnippet(m *discord.Message) string {
	if m == nil {
		return "nothing"
	}
	s := strings.Join(strings.Fields(strings.Join(useReplyTexts(m), " ")), " ")
	if len(s) > 140 {
		s = s[:140] + "..."
	}
	return fmt.Sprintf("%q", s)
}
