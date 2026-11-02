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

func TestLeavingSubscriberTakesItsEventsWithItIfOthersRemain(t *testing.T) {
	b := NewBus(8)
	stays, cancelStays := b.Subscribe()
	defer cancelStays()
	_, cancelLeaves := b.Subscribe()

	b.Send(reward(1))
	cancelLeaves()
	cancelLeaves() // safe twice
	expect(t, stays, 1)

	// A new subscriber gets that as state, not as a replayed event.
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
	feed, unsubscribe := b.Subscribe()
	defer unsubscribe()
	answer := ask(t, b, context.Background())

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
	feed, unsubscribe := b.Subscribe()
	defer unsubscribe()
	answer := ask(t, b, context.Background())
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

func TestSessionAddsUpWithNobodyWatching(t *testing.T) {
	b := NewBus(8)
	b.SetRun("classic", CurrencyCoins)
	b.Send(reward(100))
	b.Send(reward(50))
	b.Send(Event{Kind: Item, Items: map[string]int{"Ant": 2, "Boar": 1}})
	b.Send(Event{Kind: Item, Items: map[string]int{"Ant": 3}})
	b.Send(Event{Kind: Activity, Activity: "hunt"})

	got := b.Session()
	if got.Earned != 150 || got.Payouts != 2 {
		t.Fatalf("earned %d from %d payouts, want 150 from 2", got.Earned, got.Payouts)
	}
	if got.ItemCount != 6 || got.Items["Ant"] != 5 || got.Items["Boar"] != 1 {
		t.Fatalf("items = %d %v", got.ItemCount, got.Items)
	}
	if got.Activity != "hunt" {
		t.Fatalf("activity = %q", got.Activity)
	}
	if got.StartedAt == 0 || got.EndedAt != 0 {
		t.Fatalf("started %d, ended %d", got.StartedAt, got.EndedAt)
	}
}

func TestLateSubscriberGetsTheRunSoFarAndOnlyNewEvents(t *testing.T) {
	b := NewBus(8)
	b.Send(reward(100))

	feed, state, cancel := b.SubscribeSession()
	defer cancel()
	if state.Earned != 100 || len(state.Recent) != 1 {
		t.Fatalf("started with earned %d and %d rows", state.Earned, len(state.Recent))
	}
	expectIdle(t, feed)

	b.Send(reward(5))
	ev := <-feed
	if ev.Session == nil || ev.Session.Earned != 105 {
		t.Fatalf("event carried %+v, want earned 105", ev.Session)
	}
	if ev.Session.Recent != nil {
		t.Fatal("per-event state should not repeat the feed")
	}
}

func TestPerEventStateIsACopy(t *testing.T) {
	b := NewBus(8)
	feed, cancel := b.Subscribe()
	defer cancel()
	b.Send(Event{Kind: Item, Items: map[string]int{"Ant": 1}})
	first := <-feed
	b.Send(Event{Kind: Item, Items: map[string]int{"Ant": 1}})
	if got := first.Session.Items["Ant"]; got != 1 {
		t.Fatalf("earlier state changed to %d", got)
	}
}

func TestRunOnlyCountsItsOwnCurrency(t *testing.T) {
	tokens := Event{Kind: FishToken, Reward: 7}
	coins := reward(1000)

	selling := NewBus(8)
	selling.SetRun("fish", CurrencyTokens)
	selling.Send(tokens)
	selling.Send(coins)
	got := selling.Session()
	if got.Earned != 7 || got.Payouts != 1 || len(got.Recent) != 1 {
		t.Fatalf("token run: earned %d, payouts %d, rows %d", got.Earned, got.Payouts, len(got.Recent))
	}

	forCoins := NewBus(8)
	forCoins.SetRun("fish", CurrencyCoins)
	forCoins.Send(tokens)
	forCoins.Send(coins)
	if got := forCoins.Session(); got.Earned != 1000 || got.Payouts != 1 {
		t.Fatalf("coin run: earned %d, payouts %d", got.Earned, got.Payouts)
	}
}

func TestBreakClearsWhenItEnds(t *testing.T) {
	b := NewBus(8)
	start := time.Now()
	b.Send(Event{Kind: BreakStart, Reward: 300, Time: start})
	if got := b.Session().BreakUntil; got != start.Unix()+300 {
		t.Fatalf("break_until = %d, want %d", got, start.Unix()+300)
	}
	b.Send(Event{Kind: BreakEnd})
	if got := b.Session().BreakUntil; got != 0 {
		t.Fatalf("break_until = %d after the break ended", got)
	}
}

func TestFeedKeepsTheNewestRowsAndSkipsNoise(t *testing.T) {
	b := NewBus(8)
	for n := 1; n <= recentEvents+5; n++ {
		b.Send(reward(n))
		b.Send(Event{Kind: Activity, Activity: "fish"})
		b.Send(Event{Kind: CmdStart, Command: "fish"})
	}
	recent := b.Session().Recent
	if len(recent) != recentEvents {
		t.Fatalf("kept %d rows, want %d", len(recent), recentEvents)
	}
	if recent[0].Reward != 6 || recent[len(recent)-1].Reward != recentEvents+5 {
		t.Fatalf("kept rewards %d to %d", recent[0].Reward, recent[len(recent)-1].Reward)
	}
}

func TestSessionOutlivesClose(t *testing.T) {
	b := NewBus(8)
	b.Send(reward(100))
	b.Close()
	b.Close()
	b.Send(reward(1))
	got := b.Session()
	if got.Earned != 100 || got.EndedAt == 0 {
		t.Fatalf("earned %d, ended %d", got.Earned, got.EndedAt)
	}
}

func TestSessionShowsAnOpenPicker(t *testing.T) {
	b := NewBus(8)
	answer := ask(t, b, context.Background())
	if b.Session().FlowPicker == nil {
		t.Fatal("open picker missing from session")
	}
	if err := b.AnswerFlowChoice(0); err != nil {
		t.Fatal(err)
	}
	received(t, answer)
	if b.Session().FlowPicker != nil {
		t.Fatal("answered picker still in session")
	}
}

func TestPauseShowsInTheSession(t *testing.T) {
	b := NewBus(8)
	b.RequestPause(true)
	if !b.Session().Paused || !<-b.PauseSignals() {
		t.Fatal("pause not recorded and signalled")
	}
	b.RequestPause(false)
	if b.Session().Paused || <-b.PauseSignals() {
		t.Fatal("resume not recorded and signalled")
	}
}

func TestSkipBreakOnlyWorksDuringABreak(t *testing.T) {
	b := NewBus(8)
	if b.SkipBreak() {
		t.Fatal("skipped with no break running")
	}
	b.Send(Event{Kind: BreakStart, Reward: 300})
	if !b.SkipBreak() || !b.SkipBreak() {
		t.Fatal("skip refused during a break")
	}
	select {
	case <-b.BreakSkips():
	default:
		t.Fatal("skip was not signalled")
	}
	select {
	case <-b.BreakSkips():
		t.Fatal("a double click signalled twice")
	default:
	}
	b.Send(Event{Kind: BreakEnd})
	if b.SkipBreak() {
		t.Fatal("skipped after the break ended")
	}
}

func TestLeftoverSkipIsCleared(t *testing.T) {
	b := NewBus(8)
	b.Send(Event{Kind: BreakStart, Reward: 300})
	b.SkipBreak()
	b.ClearBreakSkip()
	select {
	case <-b.BreakSkips():
		t.Fatal("a leftover skip would end the next break")
	default:
	}
}
