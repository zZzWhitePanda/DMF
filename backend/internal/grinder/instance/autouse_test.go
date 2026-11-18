package instance

import (
	"testing"
	"time"

	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/config"
	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/discord"
	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/events"
)

// horseshoeReply is the reply in the screenshot that started this feature:
// text, a Use Again button, and a disabled button saying how many are left.
func horseshoeReply(left string) *discord.Message {
	return &discord.Message{
		Content: "You equip your :horseshoe: Lucky Horseshoe, giving you slightly better luck in a few commands. This will wear off in 15 minutes.",
		Components: []discord.MessageComponent{{
			Type: 1,
			Components: []discord.MessageComponent{
				{Type: 2, Label: "Use Again", CustomID: "use-again"},
				{Type: 2, Label: "You have " + left + " Lucky Horseshoe left", CustomID: "left", Disabled: true},
			},
		}},
	}
}

func TestClassifyUse(t *testing.T) {
	cases := []struct {
		name     string
		msg      *discord.Message
		want     useOutcome
		wantLeft int
	}{
		{"used, count on a disabled button", horseshoeReply("228"), useOK, 228},
		{"used, big count", horseshoeReply("1,204"), useOK, 1204},
		{"used the last one", horseshoeReply("0"), useOK, 0},
		{"used, no count", &discord.Message{Content: "You equip your Lucky Horseshoe. This will wear off in 15 minutes."}, useOK, -1},
		{"used, text in a V2 text display", &discord.Message{Components: []discord.MessageComponent{{
			Type: 17, Components: []discord.MessageComponent{{Type: 10, Content: "You ate a Pizza and feel great"}},
		}}}, useOK, -1},
		{"none owned", &discord.Message{Content: "You don't own this item??"}, useOut, 0},
		{"none owned, curly apostrophe", &discord.Message{Content: "You don’t have any Lucky Horseshoe"}, useOut, 0},
		{"unknown name", &discord.Message{Embeds: []discord.Embed{{Description: "Couldn't find an item called lucky horshoe"}}}, useUnknown, -1},
		{"still active", &discord.Message{Content: "You already have a Lucky Horseshoe active!"}, useNotReady, -1},
		{"busy with /flow", &discord.Message{Embeds: []discord.Embed{{Title: "Hold Tight!", Description: "Finish your other command first."}}}, useNotReady, -1},
		{"thinking placeholder", &discord.Message{Flags: 128}, useUnclear, -1},
		{"something else", &discord.Message{Content: "Pepe looks at you funny"}, useUnclear, -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, left := classifyUse(c.msg)
			if got != c.want || left != c.wantLeft {
				t.Errorf("classifyUse = (%d, %d), want (%d, %d)", got, left, c.want, c.wantLeft)
			}
		})
	}
}

func newTestAutoUser(t *testing.T, items ...config.AutoUseItem) (*autoUser, time.Time) {
	t.Helper()
	start := time.Unix(1_700_000_000, 0)
	a := newAutoUser(config.AutoUseConfig{Enabled: true, Items: items}, start)
	if a == nil {
		t.Fatal("newAutoUser returned nil for an enabled list")
	}
	return a, start
}

func TestNewAutoUserOffOrEmpty(t *testing.T) {
	item := config.AutoUseItem{Name: "Lucky Horseshoe", Every: config.Duration(15 * time.Minute)}
	if newAutoUser(config.AutoUseConfig{Enabled: false, Items: []config.AutoUseItem{item}}, time.Now()) != nil {
		t.Error("auto-use switched off still built a user")
	}
	if newAutoUser(config.AutoUseConfig{Enabled: true}, time.Now()) != nil {
		t.Error("an empty list still built a user")
	}
}

func TestAutoUseEveryItemDueAtStart(t *testing.T) {
	every := config.Duration(15 * time.Minute)
	a, start := newTestAutoUser(t,
		config.AutoUseItem{Name: "Lucky Horseshoe", Every: every},
		config.AutoUseItem{Name: "Pizza", Every: every},
	)
	first := a.due(start)
	if first == nil || first.name != "Lucky Horseshoe" {
		t.Fatalf("first due = %v, want Lucky Horseshoe", first)
	}
	a.record(first, useOK, 100, start)
	second := a.due(start)
	if second == nil || second.name != "Pizza" {
		t.Fatalf("second due = %v, want Pizza: every item goes before the first command", second)
	}
	a.record(second, useOK, -1, start)
	if s := a.due(start.Add(14 * time.Minute)); s != nil {
		t.Errorf("%s due before its 15 minutes were up", s.name)
	}
	if s := a.due(start.Add(15 * time.Minute)); s == nil || s.name != "Lucky Horseshoe" {
		t.Errorf("due after 15 minutes = %v, want Lucky Horseshoe", s)
	}
}

func TestAutoUseLimitHoldsUntilContinue(t *testing.T) {
	a, now := newTestAutoUser(t, config.AutoUseItem{Name: "Lucky Horseshoe", Every: config.Duration(15 * time.Minute), Limit: 2})
	for n := 1; n <= 2; n++ {
		s := a.due(now)
		if s == nil {
			t.Fatalf("use %d: nothing due", n)
		}
		st := a.record(s, useOK, 50-n, now)
		if st.used != n {
			t.Fatalf("use %d: used = %d", n, st.used)
		}
		now = now.Add(15 * time.Minute)
	}
	st := a.status()[0]
	if st.Held != events.HeldLimit || st.Used != 2 || st.NextAt != 0 || st.Left != 48 {
		t.Fatalf("after the limit: %+v, want held for limit with 2 used and 48 left", st)
	}
	if s := a.due(now.Add(time.Hour)); s != nil {
		t.Fatal("a held item came due without Continue")
	}

	if _, ok := a.resume("pizza", now); ok {
		t.Error("resumed an item that isn't in the list")
	}
	name, ok := a.resume("lucky horseshoe", now)
	if !ok || name != "Lucky Horseshoe" {
		t.Fatalf("resume = (%q, %v), want the item's own name", name, ok)
	}
	if _, ok := a.resume("Lucky Horseshoe", now); ok {
		t.Error("resumed an item that wasn't held")
	}
	st = a.status()[0]
	if st.Held != "" || st.Used != 0 || st.NextAt != now.Unix() {
		t.Fatalf("after Continue: %+v, want a fresh count, due now", st)
	}
}

func TestAutoUseOutOfItems(t *testing.T) {
	a, now := newTestAutoUser(t, config.AutoUseItem{Name: "Lucky Horseshoe", Every: config.Duration(15 * time.Minute)})

	// Using the last one counts, then holds rather than trying a /use that
	// can only fail.
	st := a.record(a.due(now), useOK, 0, now)
	if st.used != 1 || st.held != events.HeldOut {
		t.Fatalf("after the last one: used %d held %q, want 1 used and held as out", st.used, st.held)
	}

	a.resume("Lucky Horseshoe", now)
	st = a.record(a.due(now), useOut, 0, now)
	if st.used != 0 || st.held != events.HeldOut {
		t.Fatalf("after none owned: used %d held %q, want nothing counted and held as out", st.used, st.held)
	}
}

func TestAutoUseUnclearLeavesItemAlone(t *testing.T) {
	a, now := newTestAutoUser(t, config.AutoUseItem{Name: "Lucky Horseshoe", Every: config.Duration(15 * time.Minute), Limit: 3})
	for _, outcome := range []useOutcome{useUnclear, useNotReady} {
		st := a.record(a.due(now), outcome, -1, now)
		if st.used != 0 || st.held != "" {
			t.Errorf("outcome %d: used %d held %q, want neither counted nor held", outcome, st.used, st.held)
		}
	}
	st := a.record(a.due(now), useUnknown, -1, now)
	if st.held != events.HeldUnknown {
		t.Errorf("unknown name: held %q, want %q", st.held, events.HeldUnknown)
	}
}

func TestUseProgress(t *testing.T) {
	cases := []struct {
		used, limit, left int
		held              string
		want              string
	}{
		{3, 10, 225, "", "3 of 10 · 225 left · next in 15m 0s"},
		{3, 0, -1, "", "3 used · next in 15m 0s"},
		{10, 10, 1218, events.HeldLimit, "10 of 10 · 1,218 left"},
		{1, 0, 0, events.HeldOut, "1 used · 0 left"},
	}
	for _, c := range cases {
		if got := useProgress(c.used, c.limit, c.left, 15*time.Minute, c.held); got != c.want {
			t.Errorf("useProgress(%d, %d, %d, %q) = %q, want %q", c.used, c.limit, c.left, c.held, got, c.want)
		}
	}
}

// An edit of an earlier item's reply mustn't be read as the answer to this
// one, even when it arrives while this one is waiting.
func TestAwaitUseReplySkipsEditsOfOlderReplies(t *testing.T) {
	a, _ := newTestAutoUser(t, config.AutoUseItem{Name: "Pizza", Every: config.Duration(15 * time.Minute)})
	inst := &Instance{autoUse: a}

	old := horseshoeReply("228")
	old.ID = "1300000000000000100"
	a.offer(old)
	a.drainReplies()
	floor := a.replyFloor()

	edited := horseshoeReply("228")
	edited.ID = old.ID
	a.offer(edited)
	a.offer(&discord.Message{ID: "1300000000000000200", Content: "You don't own this item??"})

	outcome, left, reply := inst.awaitUseReply(t.Context(), floor, time.Second)
	if outcome != useOut || left != 0 || reply.ID != "1300000000000000200" {
		t.Fatalf("got (%d, %d, %s), want this item's own reply: out of Pizza", outcome, left, reply.ID)
	}
}
