package devcontainer

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// RunInitializeCommand runs the devcontainer initializeCommand on the host
// with the workspace as cwd, inheriting the host's environment. A non-zero
// exit code is a hard failure. An empty command is a no-op.
func RunInitializeCommand(ctx context.Context, command, workspace string) error {
	if command == "" {
		return nil
	}
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	cmd.Dir = workspace
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("initializeCommand failed: %w", err)
	}
	return nil
}

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
