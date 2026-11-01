package events

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Kind is the type of event emitted by the grinder.
type Kind int

const (
	Info Kind = iota
	Success
	Warn
	Err
	CmdStart
	CmdSuccess
	CmdFail
	CmdCooldown
	Captcha
	ShiftOn
	ShiftOff
	GatewayUp
	GatewayDown
	Activity         // current sub-command, status-line
	Item             // one or more items earned
	HighlowDecision  // we picked higher/lower/jackpot
	HighlowOutcome   // DM says we won or lost
	FlowPickerNeeded // user must choose which flow to use
	FishToken        // fish tokens earned (uses Reward field)
	FishCaught       // fish caught (uses Items field with name:1)
	RodBroken        // fishing rod broke (uses Message field with rod name)
	BreakStart       // humanized break started (Reward = duration seconds)
	BreakEnd         // humanized break ended (Reward = next session seconds)
)

// Event is one thing that happened in the grinder.
type Event struct {
	Kind     Kind
	Command  string
	Message  string
	Reward   int
	Time     time.Time
	Activity string
	Items    map[string]int
	Choices  []string
	Outcome  string

	// ID orders a run's events. Session is the run's state right after this
	// event. Send fills in both.
	ID      int64
	Session *Session
}

// Currencies a run can earn. It only ever earns one.
const (
	CurrencyCoins  = "coins"
	CurrencyTokens = "tokens"
)

// recentEvents is how much of a run's feed the bus keeps for tabs that open
// later.
const recentEvents = 200

// Session is what a run has done so far. The server keeps it so that closing
// every tab loses nothing, and all tabs show the same numbers.
type Session struct {
	Mode       string `json:"mode"`     // classic, flow or fish
	Currency   string `json:"currency"` // the one currency this run earns
	StartedAt  int64  `json:"started_at"`
	EndedAt    int64  `json:"ended_at"`    // 0 while running
	ServerTime int64  `json:"server_time"` // when this was taken, so clients can correct their clock

	Earned    int            `json:"earned"`
	Payouts   int            `json:"payouts"`
	Fish      int            `json:"fish"`
	ItemCount int            `json:"item_count"`
	Items     map[string]int `json:"items"`

	Activity      string      `json:"activity"`
	Account       string      `json:"account"`
	BreakUntil    int64       `json:"break_until"` // 0 unless on a break
	OffShift      bool        `json:"off_shift"`
	Captcha       bool        `json:"captcha"`
	GatewayDown   bool        `json:"gateway_down"`
	DiscordUp     bool        `json:"discord_up"`
	TokenRejected bool        `json:"token_rejected"`
	FlowPicker    *FlowPicker `json:"flow_picker"`

	// Recent is the feed so far, oldest first. Only set by Bus.Session and
	// SubscribeSession, so per-event copies stay small.
	Recent []Event `json:"-"`
}

// OutcomeAuthFailed marks a GatewayDown event as Discord refusing the token.
const OutcomeAuthFailed = "auth_failed"

// FlowPicker is a flow choice the grinder is blocked on.
type FlowPicker struct {
	Message string   `json:"message"`
	Choices []string `json:"choices"`
}

var (
	// ErrFlowPickerOpen means AskFlowChoice found a picker already waiting.
	ErrFlowPickerOpen = errors.New("flow picker already open")
	// ErrNoFlowPicker means an answer arrived with nothing waiting for it.
	ErrNoFlowPicker = errors.New("no flow picker open")
	// ErrBadFlowChoice means an answer isn't one of the open picker's choices.
	ErrBadFlowChoice = errors.New("flow choice out of range")
)

// Bus is a non-blocking channel-based event sink with back-channels for
// the flow picker and user pause/resume signals. Every subscriber gets its
// own copy of each event. It also keeps the run's Session, and that survives
// Close so the last run stays readable.
type Bus struct {
	buf         int
	flowAnswer  chan int
	pauseSignal chan bool // TUI → instances; true = pause, false = resume

	subMu   sync.Mutex // guards everything down to recent
	subs    map[chan Event]struct{}
	closed  bool
	lastID  int64
	session Session
	recent  []Event

	flowMu     sync.Mutex
	flowPicker *FlowPicker // the picker being waited on, nil if none
}

// NewBus returns a Bus with the given buffer size (defaults to 256).
func NewBus(buf int) *Bus {
	if buf <= 0 {
		buf = 256
	}
	return &Bus{
		buf:         buf,
		flowAnswer:  make(chan int, 1),
		pauseSignal: make(chan bool, 8),
		subs:        make(map[chan Event]struct{}),
		session:     Session{StartedAt: time.Now().Unix(), Items: map[string]int{}},
	}
}

// SetRun records what kind of run this is. Call before the grinder starts.
func (b *Bus) SetRun(mode, currency string) {
	b.subMu.Lock()
	defer b.subMu.Unlock()
	b.session.Mode = mode
	b.session.Currency = currency
}

// RequestPause asks all subscribed instances to pause (true) or resume (false).
func (b *Bus) RequestPause(pause bool) {
	if b == nil {
		return
	}
	select {
	case b.pauseSignal <- pause:
	default:
	}
}

// PauseSignals returns the receive-side of the pause/resume stream.
func (b *Bus) PauseSignals() <-chan bool {
	if b == nil {
		return nil
	}
	return b.pauseSignal
}

// Send never blocks: a subscriber whose buffer is full misses the event, but
// the next event's Session carries the state it missed. Rewards in the other
// currency are dropped: a fishing run sells for one or the other, so anything
// else is a false match. Safe on a nil bus (noop).
func (b *Bus) Send(e Event) {
	if b == nil {
		return
	}
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	b.subMu.Lock()
	defer b.subMu.Unlock()
	if b.closed || b.isOtherCurrency(e) {
		return
	}
	b.lastID++
	e.ID = b.lastID
	b.apply(e)
	if e.Kind != Activity && e.Kind != CmdStart {
		b.recent = append(b.recent, e)
		if len(b.recent) > recentEvents {
			b.recent = b.recent[1:]
		}
	}
	if len(b.subs) == 0 {
		return
	}
	state := b.snapshot()
	e.Session = &state
	for sub := range b.subs {
		offer(sub, e)
	}
}

func (b *Bus) isOtherCurrency(e Event) bool {
	switch b.session.Currency {
	case CurrencyCoins:
		return e.Kind == FishToken
	case CurrencyTokens:
		return e.Kind == CmdSuccess
	}
	return false
}

// apply folds an event into the session. Callers hold subMu.
func (b *Bus) apply(e Event) {
	s := &b.session
	switch e.Kind {
	case CmdSuccess, FishToken:
		s.Earned += e.Reward
		s.Payouts++
		s.Captcha = false
	case Item:
		b.collect(e.Items)
	case FishCaught:
		s.Fish += b.collect(e.Items)
	case Activity:
		s.Activity = e.Activity
	case Captcha:
		s.Captcha = true
	case BreakStart:
		s.BreakUntil = e.Time.Unix() + int64(e.Reward)
	case BreakEnd:
		s.BreakUntil = 0
	case ShiftOff:
		s.OffShift = true
	case ShiftOn:
		s.OffShift = false
	case GatewayUp:
		s.GatewayDown = false
		s.DiscordUp = true
		s.TokenRejected = false
		if e.Message != "" {
			s.Account = e.Message
		}
	case GatewayDown:
		s.GatewayDown = true
		s.TokenRejected = e.Outcome == OutcomeAuthFailed
	}
}

func (b *Bus) collect(items map[string]int) int {
	total := 0
	for name, qty := range items {
		b.session.Items[name] += qty
		total += qty
	}
	b.session.ItemCount += total
	return total
}

// snapshot copies the session without the feed. Callers hold subMu.
func (b *Bus) snapshot() Session {
	s := b.session
	s.ServerTime = time.Now().Unix()
	s.Items = make(map[string]int, len(b.session.Items))
	for name, qty := range b.session.Items {
		s.Items[name] = qty
	}
	b.flowMu.Lock()
	s.FlowPicker = b.flowPicker
	b.flowMu.Unlock()
	return s
}

func (b *Bus) snapshotWithFeed() Session {
	s := b.snapshot()
	s.Recent = append([]Event(nil), b.recent...)
	return s
}

// Session returns the run's state so far, feed included. Nil-safe.
func (b *Bus) Session() Session {
	if b == nil {
		return Session{}
	}
	b.subMu.Lock()
	defer b.subMu.Unlock()
	return b.snapshotWithFeed()
}

// TokenRejected reports whether Discord refused this run's token.
func (b *Bus) TokenRejected() bool {
	if b == nil {
		return false
	}
	b.subMu.Lock()
	defer b.subMu.Unlock()
	return b.session.TokenRejected
}

// Subscribe returns a channel of every event sent from now on, and a function
// to call when done reading. The channel closes when the bus does, after any
// queued events have been read.
func (b *Bus) Subscribe() (<-chan Event, func()) {
	feed, _, cancel := b.SubscribeSession()
	return feed, cancel
}

// SubscribeSession is Subscribe plus the state at that exact moment, so a
// client starts complete and no event is missed or counted twice.
func (b *Bus) SubscribeSession() (<-chan Event, Session, func()) {
	b.subMu.Lock()
	defer b.subMu.Unlock()
	sub := make(chan Event, b.buf)
	state := b.snapshotWithFeed()
	if b.closed {
		close(sub)
		return sub, state, func() {}
	}
	b.subs[sub] = struct{}{}
	return sub, state, func() { b.unsubscribe(sub) }
}

func (b *Bus) unsubscribe(sub chan Event) {
	b.subMu.Lock()
	defer b.subMu.Unlock()
	delete(b.subs, sub)
}

// Close ends every subscription. Further sends are dropped. Safe to call twice.
func (b *Bus) Close() {
	b.subMu.Lock()
	defer b.subMu.Unlock()
	if !b.closed {
		b.session.EndedAt = time.Now().Unix()
	}
	b.closed = true
	for sub := range b.subs {
		close(sub)
		delete(b.subs, sub)
	}
}

// offer sends e unless ch is full.
func offer(ch chan Event, e Event) {
	select {
	case ch <- e:
	default:
	}
}

// FlowPicker returns the picker the grinder is waiting on, or nil.
func (b *Bus) FlowPicker() *FlowPicker {
	if b == nil {
		return nil
	}
	b.flowMu.Lock()
	defer b.flowMu.Unlock()
	return b.flowPicker
}

// AnswerFlowChoice is called by the web API when the user picks a flow.
// idx is the chosen index, or -1 to cancel. With no picker open it returns
// ErrNoFlowPicker and stores nothing: the channel is buffered, so a second
// click or a stale tab would otherwise become the next picker's answer.
func (b *Bus) AnswerFlowChoice(idx int) error {
	if b == nil {
		return ErrNoFlowPicker
	}
	b.flowMu.Lock()
	defer b.flowMu.Unlock()
	if b.flowPicker == nil {
		return ErrNoFlowPicker
	}
	if idx < -1 || idx >= len(b.flowPicker.Choices) {
		return ErrBadFlowChoice
	}
	b.flowPicker = nil
	// Drain any stale value first.
	select {
	case <-b.flowAnswer:
	default:
	}
	select {
	case b.flowAnswer <- idx:
	default:
	}
	return nil
}

// AskFlowChoice is called by the grinder to show the user a flow picker and
// block until they answer. An answer only wakes one waiter, and the gateway
// and resync paths can replay a picker message on several goroutines, so
// while a picker is open further calls return ErrFlowPickerOpen.
func (b *Bus) AskFlowChoice(ctx context.Context, message string, choices []string) (int, error) {
	if b == nil {
		return -1, context.Canceled
	}
	b.flowMu.Lock()
	if b.flowPicker != nil {
		b.flowMu.Unlock()
		return -1, ErrFlowPickerOpen
	}
	b.flowPicker = &FlowPicker{Message: message, Choices: choices}
	b.flowMu.Unlock()

	b.Send(Event{Kind: FlowPickerNeeded, Message: message, Choices: choices})

	select {
	case <-ctx.Done():
		b.flowMu.Lock()
		b.flowPicker = nil
		b.flowMu.Unlock()
		return -1, ctx.Err()
	case idx := <-b.flowAnswer:
		return idx, nil
	}
}
