package instance

import (
	"hash/fnv"
	"strings"

	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/discord"
)

const flagEphemeral = 64

// foreignCommand reports whether m is Dank Memer's reply to a slash command
// the farm doesn't run, such as /advancements, /balance or /use typed on the
// same account. Those show balances and costs, not earnings.
func (i *Instance) foreignCommand(m *discord.Message) bool {
	if m.Interaction == nil || m.Interaction.Type != discord.InteractionTypeApplicationCmd {
		return false
	}
	// Subcommands come through as "fish catch".
	name, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(m.Interaction.Name)), " ")
	for _, c := range i.Commands {
		if c.Name() == name {
			return false
		}
	}
	return true
}

// earnsFrom reports whether coins and items in m count toward the run: the
// farm's own commands, buttons it pressed, and ephemeral popups, which only
// this account can see. Random events and other people's chatter don't.
func (i *Instance) earnsFrom(m *discord.Message) bool {
	if m.Interaction == nil {
		return m.Flags&flagEphemeral != 0
	}
	return !i.foreignCommand(m)
}

// rewardTextSum fingerprints the text a reward is read from, so the same
// message in the same words is only counted once.
func rewardTextSum(m *discord.Message) uint64 {
	h := fnv.New64a()
	write := func(s string) {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	write(m.Content)
	for _, e := range m.Embeds {
		write(e.Title)
		write(e.Description)
		for _, f := range e.Fields {
			write(f.Name)
			write(f.Value)
		}
		if e.Footer != nil {
			write(e.Footer.Text)
		}
	}
	for _, t := range discord.CollectText(m.Components) {
		write(t)
	}
	return h.Sum64()
}
