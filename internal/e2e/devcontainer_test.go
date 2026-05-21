//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"os"
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

// TestE2E_DevcontainerDockerfileBuild verifies that a devcontainer.json with a
// "build.dockerfile" key causes moat to build the image and run the resulting
// container.  The custom binary "moat-marker" installed by the Dockerfile must
// be present and executable.
func TestE2E_DevcontainerDockerfileBuild(t *testing.T) {
	requireDocker(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	workspace := copyDevcontainerFixture(t, "devcontainer/dockerfile-build")

	mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: &[]bool{true}[0]})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	r, err := mgr.Create(ctx, run.Options{
		Name:      "e2e-dc-dockerfile-build",
		Workspace: workspace,
		Cmd:       []string{"moat-marker"},
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

	time.Sleep(100 * time.Millisecond)

	store, err := storage.NewRunStore(storage.DefaultBaseDir(), r.ID)
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}

	logs, err := store.ReadLogs(0, 100)
	if err != nil {
		t.Fatalf("ReadLogs: %v", err)
	}

	found := false
	for _, entry := range logs {
		if strings.Contains(entry.Line, "moat-was-here") {
			found = true
			t.Logf("moat-marker output: %s", entry.Line)
			break
		}
	}
	if !found {
		t.Errorf("custom binary moat-marker missing from built container\nLogs:%s", formatLogEntries(logs))
	}
}

// TestE2E_DevcontainerFullLifecycle verifies that all four devcontainer lifecycle
// commands execute at the correct stage:
//
//   - initializeCommand  — runs on the host before the container starts; creates
//     initialize.host in the workspace directory.
//   - onCreateCommand    — runs inside the container once after creation; creates
//     onCreate.in in the container workspace folder (mounted from host).
//   - postCreateCommand  — runs inside the container after creation (same as above
//     for moat's single-start model); creates postCreate.in.
//   - postStartCommand   — runs inside the container every time the container
//     starts; creates postStart.in.
//
// All marker files are visible from the host via the workspace bind-mount.
func TestE2E_DevcontainerFullLifecycle(t *testing.T) {
	requireDocker(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	workspace := copyDevcontainerFixture(t, "devcontainer/full-lifecycle")

	mgr, err := run.NewManagerWithOptions(run.ManagerOptions{NoSandbox: &[]bool{true}[0]})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	r, err := mgr.Create(ctx, run.Options{
		Name:      "e2e-dc-full-lifecycle",
		Workspace: workspace,
		Cmd:       []string{"true"},
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

	// Give storage and filesystem time to flush.
	time.Sleep(100 * time.Millisecond)

	// All markers must be visible from the host workspace directory because the
	// workspace is bind-mounted into the container.
	markers := []string{
		"initialize.host", // created by initializeCommand on the host
		"onCreate.in",     // created by onCreateCommand inside the container
		"postCreate.in",   // created by postCreateCommand inside the container
		"postStart.in",    // created by postStartCommand inside the container
	}
	for _, m := range markers {
		path := filepath.Join(workspace, m)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("lifecycle marker %q missing at %s: %v", m, path, err)
		}
	}
}
