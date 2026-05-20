package devcontainer

import (
	"fmt"
	"strings"
)

// parseLifecycleCommand normalizes the three shapes the devcontainer spec
// allows — string, array, object — into a single shell-ready string.
// Returns "" if the input is nil or an unrecognized shape.
func parseLifecycleCommand(raw any) string {
	switch v := raw.(type) {
	case nil:
		return ""
	case string:
		return v
	case []any:
		parts := make([]string, 0, len(v))
		for _, x := range v {
			parts = append(parts, fmt.Sprint(x))
		}
		return strings.Join(parts, " ")
	case map[string]any:
		cmds := make([]string, 0, len(v))
		for _, val := range v {
			cmds = append(cmds, parseLifecycleCommand(val))
		}
		return strings.Join(cmds, " && ")
	default:
		return ""
	}
}
