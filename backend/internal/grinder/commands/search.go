package commands

import (
	"context"
	"math/rand"
	"time"

	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/discord"
	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/instance"
)

// Search picks a random location button from Dank Memer's /search response.
type Search struct{}

func (*Search) Name() string                   { return "search" }
func (*Search) DefaultCooldown() time.Duration { return 20 * time.Second }

func (*Search) Run(ctx context.Context, i *instance.Instance) (time.Duration, error) {
	if err := i.InvokeSlashCommand("search", nil); err != nil {
		return 25 * time.Second, err
	}
	return 20 * time.Second, nil
}

func (*Search) OnResponse(ctx context.Context, i *instance.Instance, m *discord.Message) error {
	// Gate: only react to messages belonging to our own /search invocation.
	// Prevents clicking random buttons on unrelated DM responses (/balance,
	// /inventory, etc.) that happen to be in the channel.
	if !isOurCommandMessage(m, "search", i.SelfUserID) {
		return nil
	}

	btns := discord.CollectButtons(m.Components)
	if len(btns) == 0 {
		return nil
	}
	pick := btns[rand.Intn(len(btns))]
	time.Sleep(time.Duration(500+rand.Intn(1500)) * time.Millisecond)
	return i.ClickButton(m.ID, pick.CustomID)
}
