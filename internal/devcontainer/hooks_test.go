package devcontainer

import "testing"

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
