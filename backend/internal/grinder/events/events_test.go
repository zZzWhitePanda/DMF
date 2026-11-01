package events

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

var choices = []string{"Grinder", "Fisher"}

func reward(n int) Event {
	return Event{Kind: CmdSuccess, Reward: n}
}

// expect reads the given rewards off feed, in order.
func expect(t *testing.T, feed <-chan Event, rewards ...int) {
	t.Helper()
	for _, want := range rewards {
		select {
		case ev, ok := <-feed:
			if !ok {
				t.Fatalf("feed closed, want reward %d", want)
			}
			if ev.Reward != want {
				t.Fatalf("got reward %d, want %d", ev.Reward, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("no event, want reward %d", want)
		}
	}
}

func expectIdle(t *testing.T, feed <-chan Event) {
	t.Helper()
	select {
	case ev, ok := <-feed:
		t.Fatalf("got %+v (open %v), want nothing", ev, ok)
	default:
	}
}

func expectClosed(t *testing.T, feed <-chan Event) {
	t.Helper()
	select {
	case ev, ok := <-feed:
		if ok {
			t.Fatalf("got %+v, want the feed closed", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("feed still open")
	}
}

func TestEverySubscriberGetsEveryEvent(t *testing.T) {
	b := NewBus(8)
	first, cancelFirst := b.Subscribe()
	defer cancelFirst()
	second, cancelSecond := b.Subscribe()
	defer cancelSecond()

	b.Send(reward(1))
	b.Send(reward(2))
	b.Send(reward(3))

	expect(t, first, 1, 2, 3)
	expect(t, second, 1, 2, 3)
}

func TestFirstSubscriberTakesWhatQueued(t *testing.T) {
	b := NewBus(8)
	b.Send(reward(1))
	b.Send(reward(2))

	first, cancelFirst := b.Subscribe()
	defer cancelFirst()
	second, cancelSecond := b.Subscribe()
	defer cancelSecond()
	expect(t, first, 1, 2)
	expectIdle(t, second)

	b.Send(reward(3))
	expect(t, first, 3)
	expect(t, second, 3)
}

func TestQueueSurvivesFillingUp(t *testing.T) {
	b := NewBus(2)
	for n := 1; n <= 10; n++ {
		b.Send(reward(n))
	}
	feed, cancel := b.Subscribe()
	defer cancel()
	expect(t, feed, 1, 2)
	expectIdle(t, feed)
}

func TestEventsQueueAgainOnceNobodyIsSubscribed(t *testing.T) {
	b := NewBus(8)
	gone, cancel := b.Subscribe()
	expectIdle(t, gone)
	b.Send(reward(1)) // sent to the subscriber, which never read it
	cancel()
	b.Send(reward(2))

	feed, cancelNext := b.Subscribe()
	defer cancelNext()
	expect(t, feed, 1, 2)
}

func TestLeavingSubscriberTakesItsEventsWithItIfOthersRemain(t *testing.T) {
	b := NewBus(8)
	stays, cancelStays := b.Subscribe()
	defer cancelStays()
	_, cancelLeaves := b.Subscribe()

	b.Send(reward(1))
	cancelLeaves()
	cancelLeaves() // safe twice
	expect(t, stays, 1)

	// Nothing went back on the queue, so a new subscriber starts clean.
	late, cancelLate := b.Subscribe()
	defer cancelLate()
	expectIdle(t, late)
}

func TestFullSubscriberDoesNotHoldUpSendOrOthers(t *testing.T) {
	b := NewBus(2)
	stuck, cancelStuck := b.Subscribe()
	defer cancelStuck()
	live, cancelLive := b.Subscribe()
	defer cancelLive()

	for n := 1; n <= 10; n++ {
		b.Send(reward(n))
		expect(t, live, n)
	}
	expect(t, stuck, 1, 2)
	expectIdle(t, stuck)
}

func TestCloseEndsSubscribersAfterTheirQueuedEvents(t *testing.T) {
	b := NewBus(8)
	first, cancelFirst := b.Subscribe()
	second, cancelSecond := b.Subscribe()

	b.Send(reward(1))
	b.Close()
	b.Close() // safe twice
	b.Send(reward(2))

	expect(t, first, 1)
	expectClosed(t, first)
	expect(t, second, 1)
	expectClosed(t, second)

	cancelFirst() // safe after close
	cancelSecond()
}

func TestSubscribingAfterCloseEndsAtOnce(t *testing.T) {
	b := NewBus(8)
	b.Close()
	b.Send(reward(1))

	feed, cancel := b.Subscribe()
	defer cancel()
	expectClosed(t, feed)
}

func TestConcurrentUseAcrossClose(t *testing.T) {
	b := NewBus(4)
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for n := range 2000 {
				b.Send(reward(n))
			}
		}()
		go func() {
			defer wg.Done()
			for range 200 {
				feed, cancel := b.Subscribe()
				select {
				case <-feed:
				default:
				}
				cancel()
			}
		}()
	}
	b.Close()
	wg.Wait()
}

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

	feed, unsubscribe := b.Subscribe()
	defer unsubscribe()
	ev := <-feed
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
	feed, unsubscribe := b.Subscribe()
	defer unsubscribe()
	<-feed

	idx, err := b.AskFlowChoice(context.Background(), "pick one", choices)
	if !errors.Is(err, ErrFlowPickerOpen) || idx != -1 {
		t.Fatalf("replay got (%d, %v), want ErrFlowPickerOpen", idx, err)
	}
	if len(feed) != 0 {
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
