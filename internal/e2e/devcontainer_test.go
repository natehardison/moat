//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/run"
	"github.com/majorcontext/moat/internal/storage"
)

// copyDevcontainerFixture copies a testdata fixture into a fresh temp dir and
// returns the path. The fixture is expected to live under
// internal/e2e/testdata/<name>.
func copyDevcontainerFixture(t *testing.T, name string) string {
	t.Helper()
	dst := t.TempDir()
	src := filepath.Join("testdata", name)
	if err := exec.Command("cp", "-R", src+"/.", dst).Run(); err != nil {
		t.Fatalf("copyDevcontainerFixture %q: %v", name, err)
	}
	return dst
}

// TestE2E_DevcontainerImageOnly verifies that a devcontainer.json with a plain
// "image" field is used as the container image and that the workspace is mounted
// at /workspaces/<name> (the devcontainer-spec default).
func TestE2E_DevcontainerImageOnly(t *testing.T) {
	requireDocker(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	workspace := copyDevcontainerFixture(t, "devcontainer/image-only")

	mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: &[]bool{true}[0]})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	r, err := mgr.Create(ctx, run.Options{
		Name:      "e2e-dc-image-only",
		Workspace: workspace,
		Cmd:       []string{"pwd"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer mgr.Destroy(context.Background(), r.ID)

	if err := mgr.Start(ctx, r.ID, run.StartOptions{}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := mgr.Wait(ctx, r.ID); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	// Give storage a moment to flush.
	time.Sleep(100 * time.Millisecond)

	store, err := storage.NewRunStore(storage.DefaultBaseDir(), r.ID)
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}

	logs, err := store.ReadLogs(0, 100)
	if err != nil {
		t.Fatalf("ReadLogs: %v", err)
	}

	// The devcontainer spec mounts the workspace at /workspaces/<basename>.
	found := false
	for _, entry := range logs {
		if strings.Contains(entry.Line, "/workspaces/") {
			found = true
			t.Logf("workspace path: %s", entry.Line)
			break
		}
	}
	if !found {
		t.Errorf("workspace not mounted at /workspaces/<name>\nLogs:%s", formatLogEntries(logs))
	}
}
