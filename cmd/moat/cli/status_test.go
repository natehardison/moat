package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/devcontainer"
	"github.com/majorcontext/moat/internal/run"
)

func TestWriteDriftHints_NoDrift(t *testing.T) {
	workspace := t.TempDir()
	dcDir := filepath.Join(workspace, ".devcontainer")
	if err := os.MkdirAll(dcDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dcDir, "devcontainer.json"), []byte(`{"image":"ubuntu:24.04"}`), 0o644); err != nil {
		t.Fatalf("write devcontainer.json: %v", err)
	}

	hash, err := devcontainer.ContentHash(workspace)
	if err != nil {
		t.Fatalf("ContentHash: %v", err)
	}

	activeRuns := []*run.Run{
		{
			Name:             "test-run",
			Workspace:        workspace,
			DevcontainerHash: hash, // same hash — no drift
		},
	}

	var buf bytes.Buffer
	writeDriftHints(&buf, activeRuns)

	if buf.Len() != 0 {
		t.Errorf("expected no drift hint when hash matches, got: %q", buf.String())
	}
}

func TestWriteDriftHints_WithDrift(t *testing.T) {
	workspace := t.TempDir()
	dcDir := filepath.Join(workspace, ".devcontainer")
	if err := os.MkdirAll(dcDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dcDir, "devcontainer.json"), []byte(`{"image":"ubuntu:24.04"}`), 0o644); err != nil {
		t.Fatalf("write devcontainer.json: %v", err)
	}

	// Capture hash of original content
	oldHash, err := devcontainer.ContentHash(workspace)
	if err != nil {
		t.Fatalf("ContentHash: %v", err)
	}

	// Now mutate devcontainer.json to simulate drift
	if err := os.WriteFile(filepath.Join(dcDir, "devcontainer.json"), []byte(`{"image":"ubuntu:22.04"}`), 0o644); err != nil {
		t.Fatalf("write mutated devcontainer.json: %v", err)
	}

	activeRuns := []*run.Run{
		{
			Name:             "drifted-run",
			Workspace:        workspace,
			DevcontainerHash: oldHash, // stale hash — workspace has changed
		},
	}

	var buf bytes.Buffer
	writeDriftHints(&buf, activeRuns)

	output := buf.String()
	if !strings.Contains(output, "hint:") {
		t.Errorf("expected drift hint, got: %q", output)
	}
	if !strings.Contains(output, "--rebuild") {
		t.Errorf("expected --rebuild in hint, got: %q", output)
	}
	if !strings.Contains(output, "drifted-run") {
		t.Errorf("expected run name in hint, got: %q", output)
	}
}

func TestWriteDriftHints_NoDevcontainerHash(t *testing.T) {
	// Runs without DevcontainerHash (no devcontainer used) should produce no hints.
	activeRuns := []*run.Run{
		{
			Name:             "plain-run",
			Workspace:        "/some/workspace",
			DevcontainerHash: "", // no devcontainer was used
		},
	}

	var buf bytes.Buffer
	writeDriftHints(&buf, activeRuns)

	if buf.Len() != 0 {
		t.Errorf("expected no hint for run without DevcontainerHash, got: %q", buf.String())
	}
}
