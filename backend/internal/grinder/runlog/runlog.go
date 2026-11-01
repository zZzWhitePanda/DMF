package runlog

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/events"
)

// Logger writes human-readable session logs to a file.
type Logger struct {
	f  *os.File
	mu sync.Mutex
}

// New opens (or creates+appends) the log file.
func New(path string) (*Logger, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	l := &Logger{f: f}
	l.Headerf("--- session started at %s ---", time.Now().Format("2006-01-02 15:04:05"))
	return l, nil
}

// Close releases the file handle.
func (l *Logger) Close() {
	if l == nil || l.f == nil {
		return
	}
	l.Headerf("--- session ended at %s ---", time.Now().Format("2006-01-02 15:04:05"))
	l.f.Close()
}

// Headerf writes an unprefixed banner line.
func (l *Logger) Headerf(format string, args ...any) {
	if l == nil || l.f == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintln(l.f, fmt.Sprintf(format, args...))
}

// writef writes a timestamped line.
func (l *Logger) writef(format string, args ...any) {
	if l == nil || l.f == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	ts := time.Now().Format("15:04:05")
	fmt.Fprintf(l.f, "[%s] %s\n", ts, fmt.Sprintf(format, args...))
}

// WriteEvent appends a formatted line for log-worthy events; silently skips noisy ones.
func (l *Logger) WriteEvent(e events.Event) {
	if l == nil {
		return
	}
	switch e.Kind {
	case events.GatewayUp:
		l.writef("gateway connected (%s)", e.Message)
	case events.GatewayDown:
		l.writef("gateway reconnecting: %s", e.Message)
	case events.Activity:
		// skip - too noisy for file log
	case events.CmdStart:
		// skip - redundant with activity/outcome
	case events.CmdSuccess:
		if e.Reward > 0 {
			l.writef("%s: +%d coins", e.Command, e.Reward)
		}
	case events.CmdFail:
		l.writef("%s: fail - %s", e.Command, e.Message)
	case events.CmdCooldown:
		l.writef("%s: cooldown %ds", e.Command, e.Reward)
	case events.Item:
		if len(e.Items) == 0 {
			return
		}
		parts := []string{}
		for name, qty := range e.Items {
			parts = append(parts, fmt.Sprintf("+%d %s", qty, name))
		}
		l.writef("items: %s", strings.Join(parts, ", "))
	case events.HighlowDecision:
		l.writef("highlow: %s", e.Message)
	case events.HighlowOutcome:
		l.writef("highlow: %s", e.Outcome)
	case events.Captcha:
		l.writef("CAPTCHA detected - grinder paused")
	case events.ShiftOn:
		l.writef("shift ON")
	case events.ShiftOff:
		l.writef("shift OFF (sleep)")
	case events.BreakStart:
		// Outcome: "short" or "big". Reward: duration seconds.
		kind := e.Outcome
		if kind == "" {
			kind = "humanized"
		}
		l.writef("%s break START (%ds): %s", kind, e.Reward, e.Message)
	case events.BreakEnd:
		kind := e.Outcome
		if kind == "" {
			kind = "humanized"
		}
		l.writef("%s break END", kind)
	case events.Info:
		l.writef("info: %s", e.Message)
	case events.Success:
		l.writef("ok: %s", e.Message)
	case events.Warn:
		l.writef("warn: %s", e.Message)
	case events.Err:
		l.writef("error: %s", e.Message)
	case events.FlowPickerNeeded:
		l.writef("flow picker shown (%d choices)", len(e.Choices))
	}
}
