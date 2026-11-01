package instance

import (
	_ "embed"
	"sort"
	"strings"
	"unicode"
)

// rawWordlist is the embedded English+DM wordlist. One word per line,
// mixed case; the init parse lowercases on load. Extend by appending new
// words to internal/instance/wordlist.txt — the next build picks them
// up automatically. If Dank Scrambled Eggs ships a scramble we can't
// solve, the handler logs the raw letters so missing words can be
// added here on sight.
//
//go:embed wordlist.txt
var rawWordlist string

// anagramIndex maps a sorted-letters key to all wordlist words that
// anagram-to it. Built once at init. Lookup for an unscramble: sort the
// scramble's letters, read candidates, pick the best fit.
var anagramIndex map[string][]string

func init() {
	anagramIndex = make(map[string][]string)
	for _, w := range strings.Split(rawWordlist, "\n") {
		w = strings.TrimSpace(strings.ToLower(w))
		if w == "" {
			continue
		}
		key := sortedLetters(w)
		anagramIndex[key] = append(anagramIndex[key], w)
	}
}

// sortedLetters returns the lowercase letters of s sorted alphabetically.
// Used as the anagram-equivalence key: "trivia" and "iiravT" both lower-
// case to "trivia"/"iiravt" which both sort to "aiirtv".
func sortedLetters(s string) string {
	runes := []rune(strings.ToLower(s))
	sort.Slice(runes, func(i, j int) bool { return runes[i] < runes[j] })
	return string(runes)
}

// unscrambleWord returns the best wordlist match for a single scrambled
// token. The scramble preserves case positions after shuffling — the
// Dank Scrambled Eggs event ships words that were Title Case originally
// ("Trivia" → "iiravT" with the T still capitalized), so we use the set
// of uppercase letters as a hint: candidates whose first letter matches
// an uppercase letter in the scramble rank above others.
//
// Returns the word lowercased (to match DM's response convention of
// "<words> eggs" in lowercase). Empty string if no candidates found.
func unscrambleWord(scrambled string) string {
	if scrambled == "" {
		return ""
	}
	// Collect uppercase letters as a case-pattern hint.
	upperHint := map[rune]bool{}
	for _, r := range scrambled {
		if unicode.IsUpper(r) {
			upperHint[unicode.ToLower(r)] = true
		}
	}

	// Letter-sort key.
	key := sortedLetters(stripNonLetters(scrambled))
	candidates := anagramIndex[key]
	if len(candidates) == 0 {
		return ""
	}
	if len(candidates) == 1 {
		return candidates[0]
	}

	// Rank: prefer candidates whose first letter is in the uppercase
	// hint set (Title Case original). If no hint matches, return the
	// first candidate — the wordlist is roughly common-first.
	for _, c := range candidates {
		if len(c) == 0 {
			continue
		}
		first := rune(c[0])
		if upperHint[first] {
			return c
		}
	}
	return candidates[0]
}

// stripNonLetters removes anything that isn't a letter from s. Keeps
// anagram keys clean when a scramble ships with surrounding punctuation
// or whitespace.
func stripNonLetters(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	for _, r := range s {
		if unicode.IsLetter(r) {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// unscramblePhrase splits a scrambled phrase into whitespace-separated
// tokens, unscrambles each, and returns the joined lowercase result. If
// any token fails to resolve, returns ("", false) so the handler can
// log the miss instead of sending a partial / wrong answer.
func unscramblePhrase(scrambled string) (string, bool) {
	tokens := strings.Fields(scrambled)
	if len(tokens) == 0 {
		return "", false
	}
	out := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		w := unscrambleWord(tok)
		if w == "" {
			return "", false
		}
		out = append(out, w)
	}
	return strings.Join(out, " "), true
}
