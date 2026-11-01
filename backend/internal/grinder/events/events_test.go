package events

import (
	"context"
	"errors"
	"testing"
	"time"
)

var choices = []string{"Grinder", "Fisher"}

// ask opens a picker on its own goroutine, the way the grinder does, and
// returns once it's open.
func ask(t *testing.T, b *Bus, ctx context.Context) <-chan int {
	t.Helper()
	answer := make(chan int, 1)
	go func() {
		idx, _ := b.AskFlowChoice(ctx, "pick one", choices)
		answer <- idx
	}()
	deadline := time.Now().Add(time.Second)
	for b.FlowPicker() == nil {
		if time.Now().After(deadline) {
			t.Fatal("picker never opened")
		}
		time.Sleep(time.Millisecond)
	}
	return answer
}

func received(t *testing.T, answer <-chan int) int {
	t.Helper()
	select {
	case idx := <-answer:
		return idx
	case <-time.After(time.Second):
		t.Fatal("grinder is still blocked")
		return 0
	}
}

func TestAskAnnouncesThePickerOnce(t *testing.T) {
	b := NewBus(8)
	answer := ask(t, b, context.Background())

	ev := <-b.Chan()
	if ev.Kind != FlowPickerNeeded || ev.Message != "pick one" || len(ev.Choices) != 2 {
		t.Fatalf("unexpected event: %+v", ev)
	}

	if err := b.AnswerFlowChoice(1); err != nil {
		t.Fatal(err)
	}
	if got := received(t, answer); got != 1 {
		t.Fatalf("got index %d, want 1", got)
	}
	if b.FlowPicker() != nil {
		t.Fatal("picker still open after the answer")
	}
}

func TestReplayedPickerDoesNotAddAWaiter(t *testing.T) {
	b := NewBus(8)
	answer := ask(t, b, context.Background())
	<-b.Chan()

	idx, err := b.AskFlowChoice(context.Background(), "pick one", choices)
	if !errors.Is(err, ErrFlowPickerOpen) || idx != -1 {
		t.Fatalf("replay got (%d, %v), want ErrFlowPickerOpen", idx, err)
	}
	if len(b.Chan()) != 0 {
		t.Fatal("replay announced the picker again")
	}

	if err := b.AnswerFlowChoice(0); err != nil {
		t.Fatal(err)
	}
	received(t, answer)
}

func TestAnswerWithNoPickerIsNotKept(t *testing.T) {
	b := NewBus(8)
	if err := b.AnswerFlowChoice(1); !errors.Is(err, ErrNoFlowPicker) {
		t.Fatalf("got %v, want ErrNoFlowPicker", err)
	}

	answer := ask(t, b, context.Background())
	select {
	case idx := <-answer:
		t.Fatalf("new picker was answered by a stale value: %d", idx)
	case <-time.After(50 * time.Millisecond):
	}

	if err := b.AnswerFlowChoice(0); err != nil {
		t.Fatal(err)
	}
	if got := received(t, answer); got != 0 {
		t.Fatalf("got index %d, want 0", got)
	}
}

func TestAnswerRangeKeepsThePickerOpen(t *testing.T) {
	b := NewBus(8)
	answer := ask(t, b, context.Background())

	for _, idx := range []int{-2, 2, 99} {
		if err := b.AnswerFlowChoice(idx); !errors.Is(err, ErrBadFlowChoice) {
			t.Fatalf("index %d: got %v, want ErrBadFlowChoice", idx, err)
		}
	}
	if b.FlowPicker() == nil {
		t.Fatal("a rejected answer closed the picker")
	}

	if err := b.AnswerFlowChoice(-1); err != nil {
		t.Fatal(err)
	}
	if got := received(t, answer); got != -1 {
		t.Fatalf("got index %d, want the -1 cancel", got)
	}
}

func TestCancelledContextClosesThePicker(t *testing.T) {
	b := NewBus(8)
	ctx, cancel := context.WithCancel(context.Background())
	answer := ask(t, b, ctx)

	cancel()
	if got := received(t, answer); got != -1 {
		t.Fatalf("got index %d, want -1", got)
	}
	if b.FlowPicker() != nil {
		t.Fatal("picker still open after the context ended")
	}
	if err := b.AnswerFlowChoice(0); !errors.Is(err, ErrNoFlowPicker) {
		t.Fatalf("got %v, want ErrNoFlowPicker", err)
	}
}

func TestNilBus(t *testing.T) {
	var b *Bus
	if b.FlowPicker() != nil {
		t.Fatal("nil bus has a picker")
	}
	if err := b.AnswerFlowChoice(0); !errors.Is(err, ErrNoFlowPicker) {
		t.Fatalf("got %v, want ErrNoFlowPicker", err)
	}
	if _, err := b.AskFlowChoice(context.Background(), "", nil); err == nil {
		t.Fatal("nil bus accepted a picker")
	}
}
