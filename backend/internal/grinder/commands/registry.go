package commands

import (
	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/instance"
)

// Build returns the enabled command handlers.
// Exclusive modes (take over the whole grinder):
//   - "flow" overrides everything else
//   - "fish" overrides everything else (if flow is off)
//
// Otherwise returns the independent beg/search/hunt handlers.
func Build(enabled map[string]bool) []instance.Command {
	if enabled["flow"] {
		return []instance.Command{&Flow{}}
	}
	if enabled["fish"] {
		return []instance.Command{&Fish{}}
	}
	out := []instance.Command{}
	if enabled["beg"] {
		out = append(out, &Beg{})
	}
	if enabled["search"] {
		out = append(out, &Search{})
	}
	if enabled["hunt"] {
		out = append(out, &Hunt{})
	}
	return out
}
