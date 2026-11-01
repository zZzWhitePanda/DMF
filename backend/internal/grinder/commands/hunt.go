package commands

import (
	"context"
	"math/rand"
	"time"

	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/discord"
	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/instance"
	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/parser"
)

// Hunt handles /hunt including the "you need a hunting rifle" prompt and event buttons (dragon, etc).
type Hunt struct{}

func (*Hunt) Name() string                   { return "hunt" }
func (*Hunt) DefaultCooldown() time.Duration { return 30 * time.Second }

func (*Hunt) Run(ctx context.Context, i *instance.Instance) (time.Duration, error) {
	if err := i.InvokeSlashCommand("hunt", nil); err != nil {
		return 30 * time.Second, err
	}
	return 30 * time.Second, nil
}

func (*Hunt) OnResponse(ctx context.Context, i *instance.Instance, m *discord.Message) error {
	// Gate: only react to messages belonging to our own /hunt invocation.
	// The main loop routes every DM message to this handler via pendingCmd;
	// without the gate, running /balance or /inventory in the same channel
	// would trigger the random-button fallback below on an unrelated message.
	if !isOurCommandMessage(m, "hunt", i.SelfUserID) {
		return nil
	}

	if needed, ok := parser.NeedsEquipment(m); ok && needed == "hunting rifle" {
		if i.Cfg.Features.AutoBuy.HuntingRifle {
			// /shop buy item:rifle — Discord nests the subcommand as a type=1 option
			return i.InvokeSlashCommand("shop", []map[string]interface{}{
				{
					"type": 1,
					"name": "buy",
					"options": []map[string]interface{}{
						{"type": 3, "name": "item", "value": "rifle"},
					},
				},
			})
		}
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
