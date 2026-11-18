package instance

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/config"
	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/discord"
	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/events"
)

const (
	testChannel = "100"
	testSelf    = "200"
)

// stubCommand stands in for /flow: it has the name, and ignores messages.
type stubCommand struct{ name string }

func (c stubCommand) Name() string                   { return c.name }
func (c stubCommand) DefaultCooldown() time.Duration { return time.Hour }
func (c stubCommand) Run(context.Context, *Instance) (time.Duration, error) {
	return time.Hour, nil
}
func (c stubCommand) OnResponse(context.Context, *Instance, *discord.Message) error { return nil }

// flowFarm is an instance mid-/flow, with the feed's rewards to read.
func flowFarm(t *testing.T) (*Instance, <-chan events.Event) {
	t.Helper()
	bus := events.NewBus(64)
	feed, cancel := bus.Subscribe()
	t.Cleanup(cancel)
	flow := stubCommand{"flow"}
	i := &Instance{
		ChannelID:  testChannel,
		SelfUserID: testSelf,
		Cfg:        &config.Config{},
		Bus:        bus,
		State:      NewState(),
		Commands:   []Command{flow},
		pendingCmd: flow,
		runCtx:     context.Background(),
	}
	return i, feed
}

// dankMemer builds a message from Dank Memer in the farm's channel.
func dankMemer(id, text string) discord.Message {
	return discord.Message{
		ID:        id,
		ChannelID: testChannel,
		Author:    discord.User{ID: discord.DankMemerAppID},
		Content:   text,
	}
}

func replyTo(command string, kind int, m discord.Message) discord.Message {
	m.Interaction = &discord.InteractionRef{Type: kind, Name: command, User: discord.User{ID: testSelf}}
	return m
}

func deliver(t *testing.T, i *Instance, m discord.Message) {
	t.Helper()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	i.onMessage(data)
}

// rewards drains the feed and returns every coin reward on it.
func rewards(feed <-chan events.Event) []int {
	var out []int
	for {
		select {
		case ev := <-feed:
			if ev.Kind == events.CmdSuccess {
				out = append(out, ev.Reward)
			}
		default:
			return out
		}
	}
}

func TestOnlyTheFarmsOwnRepliesCountAsEarnings(t *testing.T) {
	i, feed := flowFarm(t)
	const slash = discord.InteractionTypeApplicationCmd

	// What the user saw: /advancements prestige typed on the farm's account
	// shows the pocket balance against the prestige cost.
	deliver(t, i, replyTo("advancements prestige", slash, dankMemer("1", "Pocket Balance\n⏣ 22,908,177/125,000,000")))
	deliver(t, i, replyTo("balance", slash, dankMemer("2", "Wallet: ⏣ 450,000")))
	// A random event announcement isn't anyone's command, and may not be ours.
	deliver(t, i, dankMemer("3", "First to say \"pepe\" wins ⏣ 50,000!"))
	if got := rewards(feed); len(got) != 0 {
		t.Fatalf("counted %v from messages that aren't the farm's earnings", got)
	}

	// The flow's own message counts.
	deliver(t, i, replyTo("flow", slash, dankMemer("10", "You searched the couch and found ⏣ 5,000")))
	// A button the farm pressed, and an ephemeral popup only it can see.
	deliver(t, i, replyTo("flow-step", discord.InteractionTypeMessageComponent, dankMemer("11", "You won ⏣ 1,200")))
	popup := dankMemer("12", "Successfully sold for ⏣ 900")
	popup.Flags = flagEphemeral
	deliver(t, i, popup)
	if got := rewards(feed); len(got) != 3 || got[0] != 5000 || got[1] != 1200 || got[2] != 900 {
		t.Fatalf("rewards = %v, want [5000 1200 900]", got)
	}
}

func TestReplaysOfOneMessageCountOnce(t *testing.T) {
	i, feed := flowFarm(t)
	step := replyTo("flow", discord.InteractionTypeApplicationCmd, dankMemer("10", "You searched the couch and found ⏣ 5,000"))

	deliver(t, i, step) // MESSAGE_CREATE
	deliver(t, i, step) // MESSAGE_UPDATE that only touched buttons
	deliver(t, i, step) // the stuck detector's resync, minutes later
	if got := rewards(feed); len(got) != 1 {
		t.Fatalf("rewards = %v, want the step counted once", got)
	}

	// Dank Memer edits the same message into the next step.
	step.Content = "You begged and got ⏣ 7,500"
	deliver(t, i, step)
	deliver(t, i, step)
	if got := rewards(feed); len(got) != 1 || got[0] != 7500 {
		t.Fatalf("rewards after the edit = %v, want [7500]", got)
	}
	if got := i.State.Balance(); got != 12_500 {
		t.Fatalf("balance = %d, want 12,500", got)
	}
}

func TestForeignCommand(t *testing.T) {
	i, _ := flowFarm(t)
	slash := discord.InteractionTypeApplicationCmd
	cases := []struct {
		name string
		m    discord.Message
		want bool
	}{
		{"the farm's command", replyTo("flow", slash, dankMemer("1", "")), false},
		{"its subcommand", replyTo("Flow start", slash, dankMemer("1", "")), false},
		{"/use", replyTo("use", slash, dankMemer("1", "")), true},
		{"/advancements prestige", replyTo("advancements prestige", slash, dankMemer("1", "")), true},
		{"a button press", replyTo("anything", discord.InteractionTypeMessageComponent, dankMemer("1", "")), false},
		{"no interaction", dankMemer("1", ""), false},
	}
	for _, c := range cases {
		if got := i.foreignCommand(&c.m); got != c.want {
			t.Errorf("%s: foreignCommand = %v, want %v", c.name, got, c.want)
		}
	}
}

// The farm's loop is what sends /use, and during /flow it has to come back
// round to it: /flow is sent once and then drives itself through buttons.
func TestFlowLeavesTheLoopFreeForItems(t *testing.T) {
	i, _ := flowFarm(t)
	i.State.SetCooldown("flow", time.Now().Add(24*time.Hour)) // what the loop does after sending /flow
	if c := i.pickCommand(); c != nil {
		t.Fatalf("picked %s while /flow is running, so the loop would be stuck in it", c.Name())
	}
	every := config.Duration(15 * time.Minute)
	i.autoUse = newAutoUser(config.AutoUseConfig{Enabled: true, Items: []config.AutoUseItem{{Name: "Lucky Horseshoe", Every: every}}}, time.Now())
	if s := i.autoUse.due(time.Now()); s == nil {
		t.Fatal("no item due on the loop's next pass during /flow")
	}
}

// A resync replays only the newest few Dank Memer messages. Replies to /use
// and to commands typed by hand mustn't take those places from the flow.
func TestResyncSkipsOtherCommands(t *testing.T) {
	i, _ := flowFarm(t)
	slash := discord.InteractionTypeApplicationCmd
	for _, m := range []discord.Message{
		replyTo("use", slash, dankMemer("1", "")),
		replyTo("advancements prestige", slash, dankMemer("2", "")),
	} {
		if !i.foreignCommand(&m) {
			t.Errorf("%s would be replayed", m.Interaction.Name)
		}
	}
	flow := replyTo("flow", slash, dankMemer("3", ""))
	if i.foreignCommand(&flow) {
		t.Error("the flow's own message would be skipped")
	}
}
