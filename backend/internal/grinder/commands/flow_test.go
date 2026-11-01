package commands

import (
	"context"
	"testing"
	"time"

	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/discord"
	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/events"
	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/instance"
)

func pickerMessage() *discord.Message {
	return &discord.Message{
		ID:          "picker",
		Embeds:      []discord.Embed{{Title: "Your flows"}},
		Interaction: &discord.InteractionRef{Name: "flow"},
		Components: []discord.MessageComponent{{
			Type: 1,
			Components: []discord.MessageComponent{
				{Type: discord.ComponentButton, Label: "View", CustomID: "view-a"},
				{Type: discord.ComponentButton, Label: "View", CustomID: "view-b"},
			},
		}},
	}
}

// MESSAGE_CREATE, MESSAGE_UPDATE and both resyncs replay the picker message on
// their own goroutines. Only the first may wait on the user, or one answer
// leaves the rest blocked, which is how the watchdog itself used to hang.
func TestPickerReplaysShareOnePrompt(t *testing.T) {
	bus := events.NewBus(8)
	i := &instance.Instance{Bus: bus}
	f := &Flow{}
	m := pickerMessage()

	feed, unsubscribe := bus.Subscribe()
	defer unsubscribe()

	first := make(chan error, 1)
	go func() { first <- f.OnResponse(context.Background(), i, m) }()

	deadline := time.Now().Add(time.Second)
	for bus.FlowPicker() == nil {
		if time.Now().After(deadline) {
			t.Fatal("picker never opened")
		}
		time.Sleep(time.Millisecond)
	}

	replayed := make(chan error, 1)
	go func() { replayed <- f.OnResponse(context.Background(), i, m) }()
	select {
	case err := <-replayed:
		if err != nil {
			t.Fatalf("replay: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("replay blocked on the open picker")
	}

	ev := <-feed
	if ev.Kind != events.FlowPickerNeeded || ev.Message != "Your flows" || len(ev.Choices) != 2 {
		t.Fatalf("unexpected event: %+v", ev)
	}
	if len(feed) != 0 {
		t.Fatal("picker was announced more than once")
	}

	// -1 cancels, which returns before anything is clicked.
	if err := bus.AnswerFlowChoice(-1); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-first:
		if err != nil {
			t.Fatalf("first handler: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("answer didn't release the grinder")
	}
}

func TestExtractContextReadsV2TextAndDropsMarkup(t *testing.T) {
	m := &discord.Message{
		Embeds: []discord.Embed{{Title: "**Your flows**"}},
		Components: []discord.MessageComponent{{
			Type: 17,
			Components: []discord.MessageComponent{
				{Type: 10, Content: "<:coin:123> Grinder"},
				{Type: 10, Content: "<a:fish:456> __Fisher__"},
			},
		}},
	}
	want := "Your flows\n Grinder\n Fisher"
	if got := extractContext(m); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
