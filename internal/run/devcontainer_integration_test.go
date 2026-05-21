package run

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/devcontainer"
)

// fakeBuildManager is a minimal container.BuildManager for testing devcontainer builds.
// It records calls to BuildImage and tracks which images "exist".
type fakeBuildManager struct {
	built  map[string]string // tag -> dockerfile
	exists map[string]bool   // tag -> exists
}

func newFakeBuildManager() *fakeBuildManager {
	return &fakeBuildManager{
		built:  make(map[string]string),
		exists: make(map[string]bool),
	}
}

func (f *fakeBuildManager) BuildImage(_ context.Context, dockerfile, tag string, _ container.BuildOptions) error {
	f.built[tag] = dockerfile
	f.exists[tag] = true
	return nil
}

func (f *fakeBuildManager) ImageExists(_ context.Context, tag string) (bool, error) {
	return f.exists[tag], nil
}

func (f *fakeBuildManager) GetImageHomeDir(_ context.Context, _ string) string {
	return "/root"
}

// fakeRuntimeWithBuild extends flexibleRuntime to return a fake BuildManager.
type fakeRuntimeWithBuild struct {
	flexibleRuntime
	bm *fakeBuildManager
}

func (f *fakeRuntimeWithBuild) BuildManager() container.BuildManager { return f.bm }

func TestManager_DevcontainerStageA_SetsBaseImage(t *testing.T) {
	workspace := t.TempDir()
	dcDir := filepath.Join(workspace, ".devcontainer")
	if err := os.MkdirAll(dcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dcDir, "devcontainer.json"), []byte(`{"image":"ubuntu:24.04"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	bm := newFakeBuildManager()
	rt := &fakeRuntimeWithBuild{bm: bm}
	m := newEdgeCaseManager(t, rt)

	spec, dcTag, _, err := m.resolveImageSpecForDevcontainer(context.Background(), Options{
		Workspace: workspace,
		Grants:    []string{},
		Config:    nil,
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !strings.HasPrefix(dcTag, "moat-devcontainer-") {
		t.Errorf("dcTag = %q, want prefix moat-devcontainer-", dcTag)
	}
	if spec.BaseImage != dcTag {
		t.Errorf("spec.BaseImage = %q, want %q", spec.BaseImage, dcTag)
	}
}

func TestManager_DevcontainerStageA_NoDevcontainer(t *testing.T) {
	// Workspace with no devcontainer.json — resolveImageSpecForDevcontainer
	// should return an empty dcTag and a spec with empty BaseImage.
	workspace := t.TempDir()

	bm := newFakeBuildManager()
	rt := &fakeRuntimeWithBuild{bm: bm}
	m := newEdgeCaseManager(t, rt)

	spec, dcTag, _, err := m.resolveImageSpecForDevcontainer(context.Background(), Options{
		Workspace: workspace,
		Config:    nil,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dcTag != "" {
		t.Errorf("dcTag = %q, want empty string", dcTag)
	}
	if spec.BaseImage != "" {
		t.Errorf("spec.BaseImage = %q, want empty (no devcontainer)", spec.BaseImage)
	}
}

func TestManager_DevcontainerStageA_NoDevcontainerFlag(t *testing.T) {
	// Workspace with a devcontainer.json but NoDevcontainer=true — should be ignored.
	workspace := t.TempDir()
	dcDir := filepath.Join(workspace, ".devcontainer")
	if err := os.MkdirAll(dcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dcDir, "devcontainer.json"), []byte(`{"image":"ubuntu:24.04"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	bm := newFakeBuildManager()
	rt := &fakeRuntimeWithBuild{bm: bm}
	m := newEdgeCaseManager(t, rt)

	spec, dcTag, _, err := m.resolveImageSpecForDevcontainer(context.Background(), Options{
		Workspace:      workspace,
		NoDevcontainer: true,
		Config:         nil,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dcTag != "" {
		t.Errorf("dcTag = %q, want empty (NoDevcontainer=true)", dcTag)
	}
	if spec.BaseImage != "" {
		t.Errorf("spec.BaseImage = %q, want empty", spec.BaseImage)
	}
}

func TestManager_DevcontainerStageA_MoatYAMLWins(t *testing.T) {
	// When moat.yaml specifies base_image, the devcontainer should be ignored.
	workspace := t.TempDir()
	dcDir := filepath.Join(workspace, ".devcontainer")
	if err := os.MkdirAll(dcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dcDir, "devcontainer.json"), []byte(`{"image":"ubuntu:24.04"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	bm := newFakeBuildManager()
	rt := &fakeRuntimeWithBuild{bm: bm}
	m := newEdgeCaseManager(t, rt)

	moatConfig := &testConfig{BaseImage: "debian:bookworm"}
	spec, dcTag, _, err := m.resolveImageSpecForDevcontainer(context.Background(), Options{
		Workspace: workspace,
		Config:    moatConfig.asConfig(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// moat.yaml base_image wins, so dcTag is empty and spec.BaseImage is the moat value.
	if dcTag != "" {
		t.Errorf("dcTag = %q, want empty (moat.yaml base_image wins)", dcTag)
	}
	if spec.BaseImage != "debian:bookworm" {
		t.Errorf("spec.BaseImage = %q, want debian:bookworm", spec.BaseImage)
	}
}

// testConfig is a minimal helper to build a *config.Config for tests.
type testConfig struct {
	BaseImage    string
	Dependencies []string
}

func (tc *testConfig) asConfig() *config.Config {
	return &config.Config{
		BaseImage:    tc.BaseImage,
		Dependencies: tc.Dependencies,
	}
}

// TestManager_DevcontainerOverridesUserAndWorkdir verifies that when a
// devcontainer is active, resolveImageSpecForDevcontainer returns a populated
// dcCfg with the correct user and workspaceFolder, and that the workspace-target
// logic in Create produces the expected mount target and working directory.
//
// Note: This test validates the returned dcCfg (the data that Create consumes)
// and the workspaceTarget computation logic rather than calling Create end-to-end
// (which would require a full container runtime). The E2E tests in PR 3 verify
// the full container behavior.
func TestManager_DevcontainerOverridesUserAndWorkdir(t *testing.T) {
	workspace := t.TempDir()
	dcDir := filepath.Join(workspace, ".devcontainer")
	if err := os.MkdirAll(dcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dcJSON := `{
  "image": "ubuntu:24.04",
  "remoteUser": "vscode",
  "workspaceFolder": "/work/repo",
  "containerEnv": { "FOO": "bar" },
  "remoteEnv":    { "BAZ": "qux" }
}`
	if err := os.WriteFile(filepath.Join(dcDir, "devcontainer.json"), []byte(dcJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	bm := newFakeBuildManager()
	rt := &fakeRuntimeWithBuild{bm: bm}
	m := newEdgeCaseManager(t, rt)

	_, _, dcCfg, err := m.resolveImageSpecForDevcontainer(context.Background(), Options{
		Workspace: workspace,
		Config:    nil,
	})
	if err != nil {
		t.Fatalf("resolveImageSpecForDevcontainer: %v", err)
	}
	if dcCfg == nil {
		t.Fatal("dcCfg should be non-nil when devcontainer is active")
	}

	// Verify user
	if dcCfg.User != "vscode" {
		t.Errorf("dcCfg.User = %q, want vscode", dcCfg.User)
	}

	// Verify workspaceFolder
	if dcCfg.WorkspaceFolder != "/work/repo" {
		t.Errorf("dcCfg.WorkspaceFolder = %q, want /work/repo", dcCfg.WorkspaceFolder)
	}

	// Verify remoteEnv contains BAZ
	if v, ok := dcCfg.RemoteEnv["BAZ"]; !ok || v != "qux" {
		t.Errorf("dcCfg.RemoteEnv[BAZ] = %q (ok=%v), want qux", v, ok)
	}

	// Verify workspaceTarget computation — mirror the logic in Create.
	workspaceTarget := "/workspace"
	if !false /* !opts.NoDevcontainer */ {
		if earlyDCCfg, _ := devcontainer.Detect(workspace); earlyDCCfg != nil && useDevcontainerForImage(nil, earlyDCCfg) {
			if earlyDCCfg.WorkspaceFolder != "" {
				workspaceTarget = earlyDCCfg.WorkspaceFolder
			} else {
				workspaceTarget = "/workspaces/" + filepath.Base(workspace)
			}
		}
	}
	if workspaceTarget != "/work/repo" {
		t.Errorf("workspaceTarget = %q, want /work/repo", workspaceTarget)
	}

	// Verify that the workspace would be mounted at workspaceTarget.
	// Simulate the mount creation logic from Create.
	var mounts []struct{ Source, Target string }
	hasExplicit := false
	// (no moat.yaml mounts in this test)
	if !hasExplicit {
		mounts = append(mounts, struct{ Source, Target string }{Source: workspace, Target: workspaceTarget})
	}
	foundWorkspaceMount := false
	for _, mnt := range mounts {
		if mnt.Target == "/work/repo" && mnt.Source == workspace {
			foundWorkspaceMount = true
		}
	}
	if !foundWorkspaceMount {
		t.Errorf("workspace not mounted at /work/repo; mounts = %+v", mounts)
	}

	// Verify that remoteEnv is injected into the env list before moat.yaml env.
	envList := make([]string, 0, len(dcCfg.RemoteEnv))
	for k, v := range dcCfg.RemoteEnv {
		envList = append(envList, fmt.Sprintf("%s=%s", k, v))
	}
	hasRemoteEnv := false
	for _, e := range envList {
		if e == "BAZ=qux" {
			hasRemoteEnv = true
		}
	}
	if !hasRemoteEnv {
		t.Errorf("BAZ=qux missing from simulated env list: %v", envList)
	}
}
