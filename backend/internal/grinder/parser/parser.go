package parser

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/discord"
)

// moneyPrefixedRE matches "⏣ N" OR "<:coins:id> N" (case-insensitive emoji name).
// This is the most reliable pattern because the ⏣/coins emoji unambiguously means currency.
var moneyPrefixedRE = regexp.MustCompile(`(?i)(?:⏣|<a?:coins?:\d+>)\s*([0-9][0-9,]*)`)

// moneyKeywordRE matches "won/earned/found/got/received N" with optional custom emoji before N.
// Fallback when neither ⏣ nor <:coins:...> precedes the number.
var moneyKeywordRE = regexp.MustCompile(`(?:won|earned|found|got|received)\s+(?:<a?:[a-zA-Z0-9_]+:\d+>\s*)?([0-9][0-9,]*)`)

// Items we treat as money and skip in item extraction.
var moneyNames = map[string]bool{
	"coin": true, "coins": true, "money": true, "cash": true,
	"dollar": true, "dollars": true,
}

// Matches "N <custom-emoji> ItemName" and stops the name at " and ",
// next emoji "<", "⏣" currency char, end-of-line, or punctuation.
// This prevents "stickbug and ⏣ 6" from being captured as a single item name.
var itemLineRE = regexp.MustCompile(`(\d{1,3}(?:,\d{3})*)\s+<a?:[a-zA-Z0-9_]+:\d+>\s+([A-Za-z][A-Za-z\s'_-]*?)(?:\s+and\s+|\s*<|\s*⏣|$|\s*[.,!?:])`)

// ExtractReward pulls the first money value out of a message.
// Checks content, embed fields, AND Components V2 TextDisplay content.
func ExtractReward(m *discord.Message) (int, bool) {
	texts := candidateTexts(m)
	// Prefer ⏣-prefixed matches (highly reliable, won't match percentages or bonuses).
	for _, s := range texts {
		if n, ok := findMoneyPrefixed(s); ok {
			return n, true
		}
	}
	// Fallback to keyword-anchored match.
	for _, s := range texts {
		if n, ok := findMoneyKeyword(s); ok {
			return n, true
		}
	}
	return 0, false
}

func findMoneyPrefixed(s string) (int, bool) {
	m := moneyPrefixedRE.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(strings.ReplaceAll(m[1], ",", ""))
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

func findMoneyKeyword(s string) (int, bool) {
	m := moneyKeywordRE.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(strings.ReplaceAll(m[1], ",", ""))
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// ExtractItems pulls item name -> quantity map from a DM response.
// Filters out coin/money which is handled by ExtractReward.
// Matches on individual lines of content + embed text + Components V2 text.
func ExtractItems(m *discord.Message) (map[string]int, bool) {
	out := map[string]int{}
	for _, s := range candidateTexts(m) {
		for _, line := range strings.Split(s, "\n") {
			match := itemLineRE.FindStringSubmatch(line)
			if match == nil || len(match) < 3 {
				continue
			}
			qtyStr := strings.ReplaceAll(match[1], ",", "")
			qty, err := strconv.Atoi(qtyStr)
			if err != nil || qty <= 0 {
				continue
			}
			name := strings.TrimSpace(match[2])
			// Strip markdown bold/italic wrappers.
			name = strings.Trim(name, "*_`")
			name = strings.TrimSpace(name)
			name = strings.TrimRight(name, ".,!?:")
			if len(name) < 2 {
				continue
			}
			lower := strings.ToLower(name)
			if moneyNames[lower] {
				continue
			}
			out[lower] += qty
		}
	}
	return out, len(out) > 0
}

// LooksLikeCooldown returns true if the message indicates the command is on cooldown.
func LooksLikeCooldown(m *discord.Message) bool {
	for _, s := range candidateTexts(m) {
		l := strings.ToLower(s)
		if strings.Contains(l, "slow it down") ||
			strings.Contains(l, "chill out") ||
			strings.Contains(l, "on cooldown") ||
			(strings.Contains(l, "wait") && strings.Contains(l, "second")) {
			return true
		}
	}
	return false
}

// NeedsEquipment returns an equipment name if the response complains about missing gear.
func NeedsEquipment(m *discord.Message) (string, bool) {
	for _, s := range candidateTexts(m) {
		l := strings.ToLower(s)
		if needsItem(l, "fishing pole") {
			return "fishing pole", true
		}
		if needsItem(l, "hunting rifle") {
			return "hunting rifle", true
		}
		if needsItem(l, "shovel") {
			return "shovel", true
		}
		if needsItem(l, "laptop") {
			return "laptop", true
		}
	}
	return "", false
}

func needsItem(hay, item string) bool {
	if !strings.Contains(hay, item) {
		return false
	}
	return strings.Contains(hay, "don't have") ||
		strings.Contains(hay, "do not have") ||
		strings.Contains(hay, "need a") ||
		strings.Contains(hay, "buy a") ||
		strings.Contains(hay, "no "+item)
}

// candidateTexts gathers every text source in the message:
// content, embed title/description/fields/footer, AND Components V2 TextDisplay content.
func candidateTexts(m *discord.Message) []string {
	out := []string{m.Content}
	for _, e := range m.Embeds {
		out = append(out, e.Title, e.Description)
		for _, f := range e.Fields {
			out = append(out, f.Name, f.Value)
		}
		if e.Footer != nil {
			out = append(out, e.Footer.Text)
		}
	}
	// Components V2: walk nested containers/sections for TextDisplay text.
	out = append(out, discord.CollectText(m.Components)...)
	return out
}
