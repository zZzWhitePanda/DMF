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
}

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
// the flow picker and user pause/resume signals.
type Bus struct {
	ch          chan Event
	flowAnswer  chan int
	pauseSignal chan bool // TUI → instances; true = pause, false = resume

	flowMu     sync.Mutex
	flowPicker *FlowPicker // the picker being waited on, nil if none
}

// NewBus returns a Bus with the given buffer size (defaults to 256).
func NewBus(buf int) *Bus {
	if buf <= 0 {
		buf = 256
	}
	return &Bus{
		ch:          make(chan Event, buf),
		flowAnswer:  make(chan int, 1),
		pauseSignal: make(chan bool, 8),
	}
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

// Send drops the event if the buffer is full. Safe on a nil bus (noop).
func (b *Bus) Send(e Event) {
	if b == nil {
		return
	}
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	select {
	case b.ch <- e:
	default:
	}
}

// Chan exposes the receive-only side for consumers.
func (b *Bus) Chan() <-chan Event {
	return b.ch
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
