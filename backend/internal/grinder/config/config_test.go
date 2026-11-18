package config

import (
	"strings"
	"testing"
	"time"
)

const baseYAML = `clusters:
  - name: main
    guild_id: "1"
    channel_id: "2"
    master: {name: alpha, token: t}
`

func TestAutoUseConfig(t *testing.T) {
	cfg, err := Parse([]byte(baseYAML + `features:
  auto_use:
    enabled: true
    items:
      - {name: "  Lucky Horseshoe ", every: 15m, limit: 10}
      - {name: Pizza}
`))
	if err != nil {
		t.Fatal(err)
	}
	items := cfg.Features.AutoUse.Items
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	if items[0].Name != "Lucky Horseshoe" || items[0].Every.Std() != 15*time.Minute || items[0].Limit != 10 {
		t.Errorf("first item = %+v", items[0])
	}
	if items[1].Every.Std() != 15*time.Minute || items[1].Limit != 0 {
		t.Errorf("an item with no gap or limit = %+v, want 15m and no limit", items[1])
	}
}

func TestAutoUseConfigRejects(t *testing.T) {
	cases := map[string]string{
		"blank name":     `{name: " ", every: 15m}`,
		"too often":      `{name: Pizza, every: 30s}`,
		"negative limit": `{name: Pizza, every: 15m, limit: -1}`,
	}
	for name, item := range cases {
		t.Run(name, func(t *testing.T) {
			// Off still checks, so a list saved for later isn't broken when switched on.
			_, err := Parse([]byte(baseYAML + "features:\n  auto_use:\n    enabled: false\n    items:\n      - " + item + "\n"))
			if err == nil || !strings.Contains(err.Error(), "auto-use item 1") {
				t.Fatalf("err = %v, want it to name auto-use item 1", err)
			}
		})
	}
	_, err := Parse([]byte(baseYAML + `features:
  auto_use:
    items: [{name: Pizza, every: 15m}, {name: pizza, every: 20m}]
`))
	if err == nil || !strings.Contains(err.Error(), "already in the list") {
		t.Fatalf("duplicate names: err = %v", err)
	}
}

// A cleared field saves as an empty string. That reads as unset, so the item
// gets the default gap rather than a parse error about nanoseconds.
func TestBlankDurationIsUnset(t *testing.T) {
	cfg, err := Parse([]byte(baseYAML + "features:\n  auto_use:\n    items: [{name: Pizza, every: \"\"}]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Features.AutoUse.Items[0].Every.Std(); got != 15*time.Minute {
		t.Errorf("every = %s, want the 15m default", got)
	}
}
