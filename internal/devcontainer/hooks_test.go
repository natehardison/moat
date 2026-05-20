package devcontainer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
