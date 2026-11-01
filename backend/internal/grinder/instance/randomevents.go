package instance

import (
	"math/rand"
	"regexp"
	"strings"

	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/discord"
)

// randomEventAction tells the dispatcher what the bot should do when a
// detector matches. Exactly one field is populated:
//
//   - Texts (non-empty)  → send each entry as a message, in order, with a
//     short delay between them. Single-answer handlers (First to Say,
//     Reverse Reverse, Fortnite Dance, Scrambled Eggs) use a 1-length
//     slice. Multi-answer handlers (Trivia Night) spam all plausible
//     options until DM accepts one as correct.
//   - ButtonCustomID     → click the button with that custom_id on the
//     event message itself. Used by Punch Pepe + Steal Mel's Beard.
type randomEventAction struct {
	Texts          []string
	ButtonCustomID string
}

// randomEventHandler is one Dank Memer channel-event type the bot can
// auto-answer. Detect inspects the message and returns a non-nil action
// if it matched. Each handler has a stable config key so users can
// toggle individual auto-responses from the settings TUI.
//
// To add a new handler:
//  1. Write the detector func (returns *randomEventAction or nil).
//  2. Append an entry to randomEventHandlers below.
//  3. Add it to settingsBreaksOptions in internal/tui/settings.go via
//     randomEventToggle("<key>", "↳ <display name>").
//  4. Add the key to configs/config.yaml under random_event_handlers.
type randomEventHandler struct {
	Key    string
	Name   string
	Detect func(m *discord.Message) *randomEventAction
}

// randomEventHandlers is the ordered registry of event handlers. Instance
// dispatches in this order; the first non-nil action wins. Order isn't
// critical as long as the detectors are mutually exclusive (they each
// key off a unique event title).
var randomEventHandlers = []randomEventHandler{
	{Key: "first_to_say", Name: "First to Say", Detect: detectFirstToSay},
	{Key: "reverse_reverse", Name: "Reverse Reverse", Detect: detectReverseReverse},
	{Key: "fortnite_dance", Name: "Fortnite Dance Mode", Detect: detectFortniteDance},
	{Key: "punch_pepe", Name: "Punch Pepe", Detect: detectPunchPepe},
	{Key: "steal_mels_beard", Name: "Steal Mel's Beard", Detect: detectStealMelsBeard},
	{Key: "scrambled_eggs", Name: "Dank Scrambled Eggs", Detect: detectScrambledEggs},
	{Key: "trivia_night", Name: "Trivia Night", Detect: detectTriviaNight},
}

// ---------------------------------------------------------------------------
// First to Say — classic phrase-in-quotes event. Covers Anti-Rizz
// ("ZERO RIZZ"), Expired Human Sale ("BONE BRO"), and dozens of other
// one-off variants DM ships. Handler types the phrase verbatim.
// ---------------------------------------------------------------------------

func detectFirstToSay(m *discord.Message) *randomEventAction {
	phrase := extractEventPhrase(m)
	if phrase == "" {
		return nil
	}
	return &randomEventAction{Texts: []string{phrase}}
}

// ---------------------------------------------------------------------------
// Reverse Reverse — type the shown phrase, but with the characters
// reversed. "sad yellow shark" → "krahs wolley das".
// ---------------------------------------------------------------------------

var (
	reverseEventTitleRE = regexp.MustCompile(`(?i)reverse\s+reverse`)
	reverseEventBodyRE  = regexp.MustCompile(`(?i)typed\s+in\s+reverse`)
)

var reverseBoilerplateKeywords = []string{
	"reverse", "malfunction", "programming", "please send", "typed",
	"below", "to help", "dank", "has started", "event",
}

func detectReverseReverse(m *discord.Message) *randomEventAction {
	parts := collectMessageTexts(m)
	joined := strings.Join(parts, "\n")
	if !reverseEventTitleRE.MatchString(joined) && !reverseEventBodyRE.MatchString(joined) {
		return nil
	}
	phrase := findStandalonePhrase(parts, reverseBoilerplateKeywords, phraseFilterStrict)
	if phrase == "" {
		return nil
	}
	return &randomEventAction{Texts: []string{reverseString(phrase)}}
}

// ---------------------------------------------------------------------------
// Fortnite Dance Mode — arrow emojis in a sequence; type the directions
// as words separated by spaces, e.g. "right right left" or
// "left up down left right up".
// ---------------------------------------------------------------------------

var fortniteEventTitleRE = regexp.MustCompile(`(?i)for+t?nite\s+dance\s+mode`)
var fortniteEventBodyRE = regexp.MustCompile(`(?i)corresponding\s+direction`)

// Direction emojis come through as custom DM emojis (<a?:name:id>) or as
// Unicode arrow glyphs. We match both. Custom emoji names seen so far
// contain "left" / "right" / "up" / "down" as substrings, so a loose
// substring check per name is enough.
var customEmojiRE = regexp.MustCompile(`<a?:([a-zA-Z0-9_]+):\d+>`)

func detectFortniteDance(m *discord.Message) *randomEventAction {
	parts := collectMessageTexts(m)
	joined := strings.Join(parts, "\n")
	if !fortniteEventTitleRE.MatchString(joined) && !fortniteEventBodyRE.MatchString(joined) {
		return nil
	}
	dirs := extractDanceDirections(parts)
	if len(dirs) == 0 {
		return nil
	}
	return &randomEventAction{Texts: []string{strings.Join(dirs, " ")}}
}

// extractDanceDirections walks all text segments in order and returns
// the ordered list of direction words (left/right/up/down) matching
// either custom emoji names or Unicode arrow glyphs.
//
// Order matters: Fortnite Dance requires the EXACT sequence shown, so we
// scan segments and glyphs left-to-right as they appear in the message.
func extractDanceDirections(parts []string) []string {
	out := []string{}
	for _, p := range parts {
		if p == "" {
			continue
		}
		// Custom emojis first — they appear as <a?:name:id> tokens before
		// Discord renders them, so we see the raw name.
		for _, mm := range customEmojiRE.FindAllStringSubmatchIndex(p, -1) {
			name := strings.ToLower(p[mm[2]:mm[3]])
			if dir := emojiNameToDirection(name); dir != "" {
				out = append(out, dir)
			}
		}
		// Unicode arrows as a fallback — DM has used both at different
		// times, and a mixed message would also work.
		for _, r := range p {
			if dir := runeToDirection(r); dir != "" {
				out = append(out, dir)
			}
		}
	}
	return out
}

func emojiNameToDirection(name string) string {
	switch {
	case strings.Contains(name, "left"):
		return "left"
	case strings.Contains(name, "right"):
		return "right"
	case strings.Contains(name, "up"):
		return "up"
	case strings.Contains(name, "down"):
		return "down"
	}
	return ""
}

func runeToDirection(r rune) string {
	switch r {
	case '←', '⬅':
		return "left"
	case '→', '➡':
		return "right"
	case '↑', '⬆':
		return "up"
	case '↓', '⬇':
		return "down"
	}
	return ""
}

// ---------------------------------------------------------------------------
// Punch Pepe — 3x3 grid in the embed with pepeBoxer in one cell and
// white_small_square in the other eight. Below the embed is a 3x3 set
// of fist buttons. Click the fist whose grid position matches pepe.
//
// Grid is read as nine consecutive emoji tokens in order (row-major:
// cells 0-2 = top row, 3-5 = middle, 6-8 = bottom). Buttons flow the
// same way via discord.CollectButtons, so pepe-token-index equals
// button-index 1:1.
// ---------------------------------------------------------------------------

var punchPepeTitleRE = regexp.MustCompile(`(?i)punch\s+pepe`)

// pepeTokenRE matches any grid cell: Discord custom emoji (with name
// captured in group 1) or any of the common Unicode geometric / square
// characters DM might use as fillers. Deliberately wide — we don't care
// what specific filler DM picks, only that each grid cell matches.
// Covers: Geometric Shapes block (U+25A0-U+25FF), the large squares
// U+2B1B/U+2B1C, colored square emoji U+1F7E6-U+1F7EB, and the
// button-square variants U+1F532/U+1F533.
var pepeTokenRE = regexp.MustCompile(`<a?:([a-zA-Z0-9_]+):\d+>|[\x{25A0}-\x{25FF}\x{2B1B}-\x{2B1C}\x{1F7E6}-\x{1F7EB}\x{1F532}-\x{1F533}]`)

func detectPunchPepe(m *discord.Message) *randomEventAction {
	parts := collectMessageTexts(m)
	joinedLower := strings.ToLower(strings.Join(parts, "\n"))
	if !punchPepeTitleRE.MatchString(joinedLower) && !strings.Contains(joinedLower, "pepeboxer") {
		return nil
	}

	pos := findPepeGridPosition(parts)
	if pos < 0 || pos > 8 {
		return nil
	}

	// Pick the pos-th fist button. DM lays out 9 identical fist buttons
	// in 3 action rows, so index == grid position directly. Filter to
	// enabled, non-URL, has-custom-id buttons.
	allBtns := discord.CollectButtons(m.Components)
	actionBtns := make([]*discord.MessageComponent, 0, 9)
	for _, b := range allBtns {
		if b.URL != "" || b.CustomID == "" {
			continue
		}
		actionBtns = append(actionBtns, b)
	}
	if pos >= len(actionBtns) {
		return nil
	}
	return &randomEventAction{ButtonCustomID: actionBtns[pos].CustomID}
}

// findPepeGridPosition locates pepe in the 3x3 grid using a token-
// uniqueness heuristic: among the 9 grid cells, 8 are identical fillers
// and exactly one is pepe. Collect every emoji-ish token across all
// message text in order, then scan for a 9-token window containing
// exactly two distinct values with an 8:1 split. The 1 is pepe; its
// position within the window is the 0-8 grid index.
//
// Why this instead of matching by name:
//   - DM-agnostic: works whether pepe is named pepeBoxer, melPepe,
//     fighterPepe, or any future variation.
//   - Filler-agnostic: works whether DM uses Unicode ▫/◽/⬜ or a
//     custom emoji for the empty cells.
//   - Robust to leading/trailing emoji tokens outside the grid (header
//     icons, GIF thumbnails, reward lines in edited messages) — the
//     8+1 window search locates the actual grid even if offsets shift.
func findPepeGridPosition(parts []string) int {
	// Flatten every emoji-ish token across all text parts into one
	// ordered list of "name" strings (custom emoji name OR Unicode
	// character for fillers).
	tokens := make([]string, 0, 16)
	for _, p := range parts {
		matches := pepeTokenRE.FindAllStringSubmatchIndex(p, -1)
		for _, mm := range matches {
			var name string
			if mm[2] >= 0 {
				name = strings.ToLower(p[mm[2]:mm[3]])
			} else {
				name = p[mm[0]:mm[1]]
			}
			tokens = append(tokens, name)
		}
	}
	if len(tokens) < 9 {
		return -1
	}

	// Scan every 9-token sliding window for an 8+1 distribution.
	for start := 0; start <= len(tokens)-9; start++ {
		window := tokens[start : start+9]
		counts := map[string]int{}
		for _, name := range window {
			counts[name]++
		}
		if len(counts) != 2 {
			continue
		}
		var uniqueName string
		valid := false
		for name, c := range counts {
			if c == 1 {
				uniqueName = name
			}
			if c == 8 || c == 1 {
				valid = true
			} else {
				valid = false
				break
			}
		}
		if !valid || uniqueName == "" {
			continue
		}
		for i, name := range window {
			if name == uniqueName {
				return i
			}
		}
	}
	return -1
}

// ---------------------------------------------------------------------------
// Steal Mel's Beard — three buttons in a row, each with a different emoji.
// Click the one with the beard emoji. Identification uses the emoji name
// (containing "beard") or Unicode 🧔.
// ---------------------------------------------------------------------------

var stealBeardTitleRE = regexp.MustCompile(`(?i)steal\s+mel'?s\s+beard`)

func detectStealMelsBeard(m *discord.Message) *randomEventAction {
	parts := collectMessageTexts(m)
	joined := strings.ToLower(strings.Join(parts, "\n"))
	if !stealBeardTitleRE.MatchString(joined) && !strings.Contains(joined, "mel's beard has been spotted") {
		return nil
	}
	// Scan all buttons for one whose emoji name contains "beard", or the
	// Unicode "person: beard" cluster. First match wins (DM ships three
	// unique options, so there's exactly one correct beard).
	for _, b := range discord.CollectButtons(m.Components) {
		if b.URL != "" || b.CustomID == "" {
			continue
		}
		if b.Emoji != nil {
			name := strings.ToLower(b.Emoji.Name)
			if strings.Contains(name, "beard") {
				return &randomEventAction{ButtonCustomID: b.CustomID}
			}
		}
		// Label fallback — some DM emojis come through as text ":beard:"
		// shortcodes rather than a parsed Emoji struct.
		label := strings.ToLower(b.Label)
		if strings.Contains(label, "beard") {
			return &randomEventAction{ButtonCustomID: b.CustomID}
		}
	}
	// Detected the event but couldn't identify the beard button — log by
	// returning nil (dispatcher's role-ping fallback catches it).
	return nil
}

// ---------------------------------------------------------------------------
// Dank Scrambled Eggs — unscramble the shown phrase, send "<words> eggs".
// Uses the embedded wordlist (wordlist.txt + wordlist.go) for anagram
// lookup, with the uppercase-letter pattern of the scramble as a case
// hint when multiple candidates match.
// ---------------------------------------------------------------------------

var scrambledEggsTitleRE = regexp.MustCompile(`(?i)dank\s+scrambled\s+eggs`)
var scrambledEggsBodyRE = regexp.MustCompile(`(?i)unscramble\s+the\s+given\s+phrase`)

// scrambledBoilerplateKeywords matches text that's framing / instructions,
// never the target phrase. Lets findStandalonePhrase skip them.
var scrambledBoilerplateKeywords = []string{
	"scrambled", "unscramble", "eggs", "phrase", "example",
	"dropped", "chat", "afterwards", "would be", "you'd send",
	"my eggs", "to win", "dank", "has started", "event",
}

func detectScrambledEggs(m *discord.Message) *randomEventAction {
	parts := collectMessageTexts(m)
	joined := strings.Join(parts, "\n")
	if !scrambledEggsTitleRE.MatchString(joined) && !scrambledEggsBodyRE.MatchString(joined) {
		return nil
	}
	// Phrase sits in its own text block (large, bold rendering in Discord),
	// typically 1-3 scrambled "words". Scan backwards past the boilerplate.
	phrase := findStandalonePhrase(parts, scrambledBoilerplateKeywords, phraseFilterStrict)
	if phrase == "" {
		return nil
	}
	solved, ok := unscramblePhrase(phrase)
	if !ok {
		// Can't resolve — nil here so the role-ping warning fires with
		// the raw letters logged elsewhere. User can extend wordlist.txt
		// with the missing words.
		return nil
	}
	return &randomEventAction{Texts: []string{solved + " eggs"}}
}

// ---------------------------------------------------------------------------
// Trivia Night — multiple-choice question with 4 bulleted options. DM
// doesn't penalize wrong answers (the event is a race, not a quiz), so
// we spam all parsed options one by one until DM accepts one as correct.
// Guarantees a win as long as the right answer parses out of the options
// list. Shuffled before sending so repeated events don't always submit
// in the same order.
// ---------------------------------------------------------------------------

var triviaTitleRE = regexp.MustCompile(`(?i)trivia\s+night`)
var triviaOptionRE = regexp.MustCompile(`(?m)^\s*[•*\-•]\s+(.+?)\s*$`)

func detectTriviaNight(m *discord.Message) *randomEventAction {
	parts := collectMessageTexts(m)
	joined := strings.Join(parts, "\n")
	if !triviaTitleRE.MatchString(joined) && !strings.Contains(strings.ToLower(joined), "it's trivia night") {
		return nil
	}
	options := extractTriviaOptions(parts)
	if len(options) == 0 {
		return nil
	}
	// Shuffle so a human watching doesn't notice the same deterministic
	// order every event. The dispatcher sends sequentially with a small
	// inter-message delay.
	shuffled := make([]string, len(options))
	copy(shuffled, options)
	rand.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
	return &randomEventAction{Texts: shuffled}
}

// extractTriviaOptions walks text segments and collects bulleted lines.
// Options can appear either as "• Option" formatted lines inside a
// single TextDisplay or as separate TextDisplay nodes. Both paths are
// covered. Returns the options in display order.
func extractTriviaOptions(parts []string) []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(opt string) {
		opt = strings.TrimSpace(opt)
		if opt == "" {
			return
		}
		if seen[opt] {
			return
		}
		seen[opt] = true
		out = append(out, opt)
	}
	for _, p := range parts {
		for _, line := range strings.Split(p, "\n") {
			if match := triviaOptionRE.FindStringSubmatch(line); match != nil {
				add(match[1])
			}
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Shared helpers used by multiple detectors.
// ---------------------------------------------------------------------------

// collectMessageTexts returns every text segment on the message as a
// flat slice — content, embed title/description/fields, and Components
// V2 TextDisplay nodes. Order matches structural order so backward
// scans find the furthest-down text (where DM puts the phrase in events
// like Reverse Reverse).
func collectMessageTexts(m *discord.Message) []string {
	out := []string{m.Content}
	for _, e := range m.Embeds {
		out = append(out, e.Title, e.Description)
		for _, f := range e.Fields {
			out = append(out, f.Name, f.Value)
		}
	}
	out = append(out, discord.CollectText(m.Components)...)
	return out
}

// phraseFilter selects how strict findStandalonePhrase is about what
// qualifies as an event target phrase. phraseFilterStrict rejects any
// candidate that contains digits, Discord emoji syntax, URLs, or
// bracket characters — Reverse Reverse and Scrambled Eggs phrases are
// always plain natural-language words, so this filter prevents a
// backward scan from landing on an item line ("1 <:Apple:…> Apple") in
// an edited event-result message and treating it as the target phrase.
type phraseFilter int

const (
	phraseFilterLoose  phraseFilter = iota // boilerplate exclusion only
	phraseFilterStrict                     // + no digits/emoji/urls/brackets
)

// findStandalonePhrase scans text segments backwards (last → first) AND
// each segment's lines backwards (bottom → top) looking for the first
// one that looks like an event target phrase: a short block (1-8
// words), non-empty, containing none of the given boilerplate keywords.
//
// The line-level scan matters because DM frequently bundles the phrase
// into the SAME string as the instructional text, separated by
// newlines — e.g. an embed description like:
//
//	"My programming is malfunctioning!\n
//	 To help me reset, please send the phrase below typed in reverse!\n\n
//	 box of sand"
//
// A part-only scan would reject the whole description (contains
// boilerplate words) and never find "box of sand". The per-line scan
// finds it on its own line.
//
// Strict filter additionally rejects any line containing digits, Discord
// emoji syntax, URLs, or bracket characters — item-reward lines from
// edited event-result messages ("1 <:Apple:...> Apple") shouldn't be
// treated as a phrase.
func findStandalonePhrase(parts []string, boilerplate []string, filter phraseFilter) string {
	for i := len(parts) - 1; i >= 0; i-- {
		p := parts[i]
		if strings.TrimSpace(p) == "" {
			continue
		}
		lines := strings.Split(p, "\n")
		for j := len(lines) - 1; j >= 0; j-- {
			line := strings.TrimSpace(lines[j])
			// Strip common decorative markdown so a line like
			// "**box of sand**" doesn't get rejected on bracket filter
			// (strict mode).
			line = strings.Trim(line, "*_`~ ")
			if line == "" {
				continue
			}
			if containsAny(strings.ToLower(line), boilerplate) {
				continue
			}
			words := strings.Fields(line)
			if len(words) < 1 || len(words) > 8 {
				continue
			}
			if filter == phraseFilterStrict {
				if strings.ContainsAny(line, "0123456789") {
					continue
				}
				if strings.Contains(line, "<:") || strings.Contains(line, "<a:") {
					continue
				}
				if strings.Contains(line, "http://") || strings.Contains(line, "https://") {
					continue
				}
				if strings.ContainsAny(line, "{}[]<>@#") {
					continue
				}
			}
			return line
		}
	}
	return ""
}

func containsAny(s string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

// reverseString returns s with its characters in reverse order. Rune-
// safe so emoji and multi-byte characters survive the flip.
func reverseString(s string) string {
	runes := []rune(s)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes)
}

// randomEventHandlerEnabled reports whether a specific handler is
// enabled. Missing map entries default to TRUE so a fresh config with
// no explicit per-event section auto-handles everything. Master toggle
// is the kill switch; per-event toggles are opt-OUT.
func randomEventHandlerEnabled(cfgMap map[string]bool, key string) bool {
	if cfgMap == nil {
		return true
	}
	v, ok := cfgMap[key]
	if !ok {
		return true
	}
	return v
}
