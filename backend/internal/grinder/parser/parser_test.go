package parser

import (
	"testing"

	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/discord"
)

// v2 builds a Components V2 message: a container of text displays, which is
// how /flow draws its games.
func v2(texts ...string) *discord.Message {
	box := discord.MessageComponent{Type: 17}
	for _, t := range texts {
		box.Components = append(box.Components, discord.MessageComponent{Type: 10, Content: t})
	}
	return &discord.Message{Components: []discord.MessageComponent{box}}
}

func TestExtractReward(t *testing.T) {
	cases := []struct {
		name string
		msg  *discord.Message
		want int // 0 for no reward
	}{
		// The screen the user reported: the pocket comes first and was read
		// as earnings.
		{"snake eyes win", v2(
			"### Badge Grind\ngrinds all /flow badges\nXP multiplier: **2.64x**",
			"### zzz's winning Snake Eyes game\nPocket: ⏣ **47,223,043**\nWinnings: ⏣ **7,500**",
			"Bet: ⏣ 6,000 | Payouts: 1 eye (1.25:1) 2 eyes (12:1)",
			"7/10 | **snakeeyes** → cointoss → slots → ...",
		), 7500},
		{"snake eyes win, no bold", v2(
			"zzz's winning Snake Eyes game\nPocket: ⏣ 47,223,043\nWinnings: ⏣ 7,500",
			"Bet: ⏣ 6,000 | Payouts: 1 eye (1.25:1) 2 eyes (12:1)",
		), 7500},
		{"snake eyes loss", v2(
			"### zzz's losing Snake Eyes game\nPocket: ⏣ **47,217,043**\nLosses: ⏣ **6,000**",
			"Bet: ⏣ 6,000",
		), 0},
		{"only a balance", v2("Wallet: ⏣ 450,000\nBank: ⏣ 1,000,000"), 0},
		{"progress toward a cost", v2("Pocket Balance\n⏣ 22,908,177/125,000,000"), 0},
		{"label in an embed field name", &discord.Message{Embeds: []discord.Embed{{
			Fields: []discord.EmbedField{{Name: "Pocket", Value: "⏣ 1,000,000"}, {Name: "Winnings", Value: "⏣ 300"}},
		}}}, 300},
		{"found", &discord.Message{Content: "You searched the couch and found ⏣ 5,000"}, 5000},
		{"beg", &discord.Message{Embeds: []discord.Embed{{Description: "Pepe gave you ⏣ 1,234"}}}, 1234},
		{"won, then a balance", &discord.Message{Content: "You won ⏣ 7,500 and your wallet is now ⏣ 50,000"}, 7500},
		{"plus sign", v2("+⏣ 250"), 250},
		{"coin emoji", &discord.Message{Content: "You got <:coins:123> **900**"}, 900},
		{"unlabelled amount", &discord.Message{Content: "The crowd threw ⏣ 640 at you"}, 640},
		{"no currency symbol", &discord.Message{Content: "You won 75 coins"}, 75},
		{"nothing", &discord.Message{Content: "Pepe looks at you funny"}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			n, ok := ExtractReward(c.msg)
			if c.want == 0 {
				if ok {
					t.Fatalf("read %d as earned, want nothing", n)
				}
				return
			}
			if !ok || n != c.want {
				t.Fatalf("ExtractReward = (%d, %v), want %d", n, ok, c.want)
			}
		})
	}
}
