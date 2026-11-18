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

// notEarnedRE marks an amount that's shown, not earned: a balance, a stake,
// a price or a loss. /flow's games show "Pocket: ⏣ 47,223,043" right above
// "Winnings: ⏣ 7,500", and taking the first amount read the pocket.
var notEarnedRE = regexp.MustCompile(`\b(pocket|wallet|bank|balance|net ?worth|bets?|wager|stake|costs?|price|spent|paid|fees?|tax|lost|lose|loss|losses|total|max|maximum|limit|needs?|requires?|required|worth|deposit|deposited|withdrew|withdrawn?)\b`)

// earnedRE marks an amount as earned: "Winnings: ⏣ 7,500", "you found ⏣ 5,000".
var earnedRE = regexp.MustCompile(`\b(winnings?|won|wins?|earned|earn|found|got|received|profit|rewards?|gave you|given)\b|\+$`)

// markdownStripper drops the emphasis Dank Memer wraps around numbers, as in
// "⏣ **7,500**", which would otherwise split the symbol from the amount.
var markdownStripper = strings.NewReplacer("*", "", "__", "", "`", "", "~", "")

// shownAmount is one coin amount in a message, with what it's labelled.
type shownAmount struct {
	n     int
	label string // lowercase words before it on its line, or the line above if it stands alone
	after string // what follows the number
}

func (a shownAmount) earned() bool {
	return !a.shown() && earnedRE.MatchString(a.label)
}

// shown means a balance, stake, price or loss, or a progress figure like
// "22,908,177/125,000,000".
func (a shownAmount) shown() bool {
	return notEarnedRE.MatchString(a.label) || strings.HasPrefix(a.after, "/")
}

// ExtractReward finds the coins a message says were earned. It checks
// content, embeds and Components V2 text. An amount labelled as earned
// wins; otherwise the first amount that isn't a balance, stake, price or
// loss; otherwise a "won/found/got N" phrase with no currency symbol.
func ExtractReward(m *discord.Message) (int, bool) {
	amounts := coinAmounts(rewardSources(m))
	for _, a := range amounts {
		if a.earned() {
			return a.n, true
		}
	}
	for _, a := range amounts {
		if !a.shown() {
			return a.n, true
		}
	}
	if len(amounts) > 0 {
		return 0, false // every amount was a balance or a cost
	}
	for _, s := range candidateTexts(m) {
		if n, ok := findMoneyKeyword(s); ok {
			return n, true
		}
	}
	return 0, false
}

// rewardSources is candidateTexts with each embed field's name kept above
// its value, so "Pocket" still labels the "⏣ 1,000" under it.
func rewardSources(m *discord.Message) []string {
	out := []string{m.Content}
	for _, e := range m.Embeds {
		out = append(out, e.Title, e.Description)
		for _, f := range e.Fields {
			out = append(out, f.Name+"\n"+f.Value)
		}
		if e.Footer != nil {
			out = append(out, e.Footer.Text)
		}
	}
	return append(out, discord.CollectText(m.Components)...)
}

// coinAmounts finds every ⏣ or coin-emoji amount, with its label.
func coinAmounts(sources []string) []shownAmount {
	var out []shownAmount
	for _, src := range sources {
		above := ""
		for _, line := range strings.Split(markdownStripper.Replace(src), "\n") {
			start := 0
			for _, loc := range moneyPrefixedRE.FindAllStringSubmatchIndex(line, -1) {
				n, err := strconv.Atoi(strings.ReplaceAll(line[loc[2]:loc[3]], ",", ""))
				label := strings.ToLower(strings.TrimSpace(line[start:loc[0]]))
				if !hasLetter(label) && start == 0 {
					label = above + " " + label
				}
				start = loc[1]
				if err != nil || n <= 0 {
					continue
				}
				out = append(out, shownAmount{n: n, label: strings.TrimSpace(label), after: strings.TrimSpace(line[loc[1]:])})
			}
			if t := strings.ToLower(strings.TrimSpace(line)); t != "" {
				above = t
			}
		}
	}
	return out
}

func hasLetter(s string) bool {
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return true
		}
	}
	return false
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
