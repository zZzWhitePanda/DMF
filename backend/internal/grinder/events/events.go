package events

import (
	"context"
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

// Bus is a non-blocking channel-based event sink with back-channels for
// the flow picker and user pause/resume signals.
type Bus struct {
	ch          chan Event
	flowAnswer  chan int
	pauseSignal chan bool // TUI → instances; true = pause, false = resume
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

// AnswerFlowChoice is called by the TUI when the user picks a flow.
// idx is the chosen index, or -1 to cancel.
func (b *Bus) AnswerFlowChoice(idx int) {
	if b == nil {
		return
	}
	// Drain any stale value first.
	select {
	case <-b.flowAnswer:
	default:
	}
	select {
	case b.flowAnswer <- idx:
	default:
	}
}

// AwaitFlowChoice is called by the grinder to block until the user picks a flow.
func (b *Bus) AwaitFlowChoice(ctx context.Context) (int, error) {
	if b == nil {
		return -1, context.Canceled
	}
	select {
	case <-ctx.Done():
		return -1, ctx.Err()
	case idx := <-b.flowAnswer:
		return idx, nil
	}
}
