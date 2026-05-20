package devcontainer

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
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

// HookOpts configures the user, working dir, and env for a hook exec.
type HookOpts struct {
	User    string
	Home    string
	Workdir string
	Env     map[string]string
}

// ExecRuntime is the minimal subset of container.Runtime we need. Lets us
// stub the runtime in tests without depending on the full interface.
type ExecRuntime interface {
	Exec(ctx context.Context, id string, cmd []string, stdin []byte, stdout, stderr io.Writer) error
}

// RunHook runs a single in-container devcontainer lifecycle hook
// (onCreate/postCreate/postStart) via the runtime's Exec. A non-zero exit
// returns an error; the caller decides hard-fail vs. warn-and-continue.
func RunHook(ctx context.Context, rt ExecRuntime, containerID, name, command string, opts HookOpts, stdout, stderr io.Writer) error {
	if command == "" {
		return nil
	}
	// We can't pass workdir or env via Exec directly, so wrap the command.
	// Env exports come first, then HOME/USER, then cd into workdir, then the
	// user command — so "cd <workdir> && <command>" is a trailing substring.
	var b strings.Builder
	keys := make([]string, 0, len(opts.Env))
	for k := range opts.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "export %s=%s; ", k, shellQuote(opts.Env[k]))
	}
	if opts.Home != "" {
		fmt.Fprintf(&b, "export HOME=%s; ", shellQuote(opts.Home))
	}
	if opts.User != "" {
		fmt.Fprintf(&b, "export USER=%s; ", shellQuote(opts.User))
	}
	if opts.Workdir != "" {
		fmt.Fprintf(&b, "cd %s && ", opts.Workdir)
	}
	b.WriteString(command)
	shellCmd := []string{"/bin/sh", "-c", b.String()}
	if err := rt.Exec(ctx, containerID, shellCmd, nil, stdout, stderr); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
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
