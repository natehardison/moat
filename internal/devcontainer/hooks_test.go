package devcontainer

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/container"
)

func TestParseLifecycleCommand_String(t *testing.T) {
	got := parseLifecycleCommand("echo hi")
	if got != "echo hi" {
		t.Errorf("got %q, want %q", got, "echo hi")
	}
}

func TestParseLifecycleCommand_Array(t *testing.T) {
	got := parseLifecycleCommand([]any{"echo", "hi"})
	if got != "echo hi" {
		t.Errorf("got %q, want %q", got, "echo hi")
	}
}

func TestParseLifecycleCommand_ObjectSerializedWithAnd(t *testing.T) {
	raw := map[string]any{
		"first":  "echo a",
		"second": []any{"echo", "b"},
	}
	got := parseLifecycleCommand(raw)
	// Order is not guaranteed for map iteration. Both possibilities must contain
	// both commands joined by &&.
	if got != "echo a && echo b" && got != "echo b && echo a" {
		t.Errorf("got %q, want one of [echo a && echo b, echo b && echo a]", got)
	}
}

func TestParseLifecycleCommand_NilOrUnknown(t *testing.T) {
	if got := parseLifecycleCommand(nil); got != "" {
		t.Errorf("nil: got %q, want empty", got)
	}
	if got := parseLifecycleCommand(42); got != "" {
		t.Errorf("int: got %q, want empty", got)
	}
}

func TestRunInitializeCommand_SuccessAndCwd(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker")
	cmd := fmt.Sprintf("pwd > %q", marker)
	if err := RunInitializeCommand(context.Background(), cmd, dir); err != nil {
		t.Fatalf("RunInitializeCommand: %v", err)
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("marker: %v", err)
	}
	got := strings.TrimSpace(string(data))
	if got != dir {
		t.Errorf("pwd = %q, want %q", got, dir)
	}
}

func TestRunInitializeCommand_NonZeroExitIsHardFail(t *testing.T) {
	err := RunInitializeCommand(context.Background(), "false", t.TempDir())
	if err == nil {
		t.Fatal("expected error from `false` command")
	}
}

func TestRunInitializeCommand_EmptyIsNoop(t *testing.T) {
	if err := RunInitializeCommand(context.Background(), "", t.TempDir()); err != nil {
		t.Errorf("empty command: got err %v, want nil", err)
	}
}

type fakeExecRuntime struct {
	calls []fakeExec
	fail  bool
}

type fakeExec struct {
	id       string
	cmd      []string
	stdinLen int
}

func (f *fakeExecRuntime) Exec(ctx context.Context, id string, cmd []string, stdin []byte, stdout, stderr io.Writer) error {
	f.calls = append(f.calls, fakeExec{id, cmd, len(stdin)})
	if f.fail {
		return &container.ExecError{ExitCode: 7}
	}
	fmt.Fprintln(stdout, "ok")
	return nil
}

func TestRunHook_PassesUserHomeAndCwd(t *testing.T) {
	fr := &fakeExecRuntime{}
	out := &bytes.Buffer{}
	err := RunHook(context.Background(), fr, "ctr-1", "onCreate", "echo hi",
		HookOpts{
			User:    "vscode",
			Home:    "/home/vscode",
			Workdir: "/workspaces/repo",
			Env:     map[string]string{"PATH": "/usr/local/bin:/usr/bin"},
		}, out, out)
	if err != nil {
		t.Fatalf("RunHook: %v", err)
	}
	if len(fr.calls) != 1 {
		t.Fatalf("got %d calls, want 1", len(fr.calls))
	}
	cmd := fr.calls[0].cmd
	joined := strings.Join(cmd, " ")
	for _, want := range []string{"sh", "-c", "cd /workspaces/repo && echo hi"} {
		if !strings.Contains(joined, want) {
			t.Errorf("cmd missing %q: %v", want, cmd)
		}
	}
}

func TestRunHook_NonZeroIsErrorForRequiredHook(t *testing.T) {
	fr := &fakeExecRuntime{fail: true}
	err := RunHook(context.Background(), fr, "ctr-1", "onCreate", "false", HookOpts{}, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("expected error for failing required hook")
	}
}

func TestRunHook_EmptyCommandIsNoop(t *testing.T) {
	fr := &fakeExecRuntime{}
	if err := RunHook(context.Background(), fr, "ctr-1", "onCreate", "", HookOpts{}, io.Discard, io.Discard); err != nil {
		t.Errorf("empty: got %v", err)
	}
	if len(fr.calls) != 0 {
		t.Errorf("empty hook should not call Exec")
	}
}
