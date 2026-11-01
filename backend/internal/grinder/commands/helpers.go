package commands

import (
	"strings"

	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/discord"
)

// isOurCommandMessage returns true if m was created by our own slash
// command invocation of cmdName. DM edits a single message across every
// step of multi-step commands (/fish, /flow), and the Interaction
// field on that message pins it to the original slash command, so this
// check uniquely identifies message chains we initiated.
//
// Gate against: other users running the same command in the same
// channel (selfUserID check), AND against our handler firing on
// unrelated DM traffic — the main instance loop sets pendingCmd to
// e.g. Flow, which routes EVERY DM message to Flow.OnResponse. Without
// this gate, running /balance while the bot is grinding would see the
// grinder click Withdraw / Deposit on the /balance response.
//
// Empty cmdName matches any slash command (rare — use only when you
// genuinely want all our interaction responses).
func isOurCommandMessage(m *discord.Message, cmdName, selfUserID string) bool {
	if m == nil || m.Interaction == nil {
		return false
	}
	if cmdName != "" && !strings.EqualFold(m.Interaction.Name, cmdName) {
		return false
	}
	if selfUserID != "" && m.Interaction.User.ID != selfUserID {
		return false
	}
	return true
}
