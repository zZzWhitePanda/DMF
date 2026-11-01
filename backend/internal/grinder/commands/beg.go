package commands

import (
	"context"
	"time"

	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/discord"
	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/instance"
)

// Beg is the simplest handler — pure slash invocation, no follow-up buttons.
type Beg struct{}

func (*Beg) Name() string                   { return "beg" }
func (*Beg) DefaultCooldown() time.Duration { return 15 * time.Second }

func (*Beg) Run(ctx context.Context, i *instance.Instance) (time.Duration, error) {
	if err := i.InvokeSlashCommand("beg", nil); err != nil {
		return 20 * time.Second, err
	}
	return 15 * time.Second, nil
}

func (*Beg) OnResponse(ctx context.Context, i *instance.Instance, m *discord.Message) error {
	return nil
}
