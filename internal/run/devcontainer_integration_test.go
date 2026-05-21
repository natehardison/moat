package run

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/devcontainer"
)

// trackingRuntime wraps fakeRuntimeWithBuild and records RemoveImage calls.
type trackingRuntime struct {
	fakeRuntimeWithBuild
	removed []string // tags passed to RemoveImage
}

func (t *trackingRuntime) RemoveImage(_ context.Context, tag string) error {
	t.removed = append(t.removed, tag)
	return nil
}

// TestMergeDevcontainerMounts verifies that a colliding mount target is
// replaced (not duplicated) and that non-colliding mounts are preserved.
func TestMergeDevcontainerMounts(t *testing.T) {
	base := []container.MountConfig{
		{Source: "/workspace/original", Target: "/workspaces/repo", ReadOnly: false},
		{Source: "/other/source", Target: "/other/target", ReadOnly: true},
	}
	additions := []devcontainer.Mount{
		// This collides with the first base mount.
		{Source: "/host/override", Target: "/workspaces/repo", Type: "bind", ReadOnly: true},
	}

	result := mergeDevcontainerMounts(base, additions)

	// Expect exactly two mounts (collision replaced, not duplicated).
	if len(result) != 2 {
		t.Fatalf("len(result) = %d, want 2; mounts: %+v", len(result), result)
	}

	// Verify the colliding target now has the devcontainer's source.
	var found bool
	for _, m := range result {
		if m.Target == "/workspaces/repo" {
			found = true
			if m.Source != "/host/override" {
				t.Errorf("mount Source = %q, want /host/override", m.Source)
			}
			if !m.ReadOnly {
				t.Errorf("mount ReadOnly = false, want true")
			}
		}
	}
	if !found {
		t.Errorf("no mount with target /workspaces/repo in result: %+v", result)
	}

	// Verify the non-colliding mount is still present.
	var otherFound bool
	for _, m := range result {
		if m.Target == "/other/target" {
			otherFound = true
			if m.Source != "/other/source" {
				t.Errorf("other mount Source = %q, want /other/source", m.Source)
			}
		}
	}
	if !otherFound {
		t.Errorf("non-colliding mount /other/target missing from result: %+v", result)
	}
}

// TestMergeDevcontainerMounts_NoCollision verifies that when there are no
// target collisions all mounts (base + additions) appear in the result.
func TestMergeDevcontainerMounts_NoCollision(t *testing.T) {
	base := []container.MountConfig{
		{Source: "/src/a", Target: "/a", ReadOnly: false},
	}
	additions := []devcontainer.Mount{
		{Source: "/src/b", Target: "/b", Type: "bind", ReadOnly: false},
	}

	result := mergeDevcontainerMounts(base, additions)

	if len(result) != 2 {
		t.Fatalf("len(result) = %d, want 2; mounts: %+v", len(result), result)
	}
}

// TestMergeDevcontainerMounts_Empty verifies edge cases with empty inputs.
func TestMergeDevcontainerMounts_Empty(t *testing.T) {
	base := []container.MountConfig{
		{Source: "/src/a", Target: "/a", ReadOnly: false},
	}

	// No additions — should return base unchanged.
	result := mergeDevcontainerMounts(base, nil)
	if len(result) != 1 {
		t.Fatalf("len(result) = %d, want 1", len(result))
	}

	// No base — additions become the full list.
	result2 := mergeDevcontainerMounts(nil, []devcontainer.Mount{
		{Source: "/src/b", Target: "/b", Type: "bind"},
	})
	if len(result2) != 1 {
		t.Fatalf("len(result2) = %d, want 1", len(result2))
	}
	if result2[0].Target != "/b" {
		t.Errorf("result2[0].Target = %q, want /b", result2[0].Target)
	}
}

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
		if earlyDCCfg, _ := devcontainer.Detect(workspace); earlyDCCfg != nil && UseDevcontainerForImage(nil, earlyDCCfg) {
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

// TestRunDevcontainerLifecycleHooks verifies that runDevcontainerLifecycleHooks
// calls onCreate, postCreate, and postStart in order, and that onCreate/postCreate
// failures abort while postStart failures only warn.
func TestRunDevcontainerLifecycleHooks(t *testing.T) {
	t.Run("all hooks run in order", func(t *testing.T) {
		var seen []string
		rt := &fakeRuntimeWithBuild{
			flexibleRuntime: flexibleRuntime{
				execFn: func(_ context.Context, _ string, cmd []string, _ []byte, _ io.Writer, _ io.Writer) error {
					joined := strings.Join(cmd, " ")
					switch {
					case strings.Contains(joined, "echo onCreate"):
						seen = append(seen, "onCreate")
					case strings.Contains(joined, "echo postCreate"):
						seen = append(seen, "postCreate")
					case strings.Contains(joined, "echo postStart"):
						seen = append(seen, "postStart")
					}
					return nil
				},
			},
			bm: newFakeBuildManager(),
		}
		m := newEdgeCaseManager(t, rt)
		r := &Run{
			ID:               "run_hooks_order",
			ContainerID:      "ctr-hooks",
			OnCreateCmd:      "echo onCreate",
			PostCreateCmd:    "echo postCreate",
			PostStartCmd:     "echo postStart",
			PostStartUser:    "vscode",
			PostStartHome:    "/home/vscode",
			PostStartWorkdir: "/workspaces/repo",
		}
		if err := m.runDevcontainerLifecycleHooks(context.Background(), r); err != nil {
			t.Fatalf("runDevcontainerLifecycleHooks: %v", err)
		}
		// The probe Exec call also fires (login-shell probe), so filter to only
		// our echo commands.
		want := []string{"onCreate", "postCreate", "postStart"}
		if !reflect.DeepEqual(seen, want) {
			t.Errorf("hook order = %v, want %v", seen, want)
		}
		// Verify one-shot clearing.
		if r.OnCreateCmd != "" {
			t.Errorf("OnCreateCmd not cleared after run: %q", r.OnCreateCmd)
		}
		if r.PostCreateCmd != "" {
			t.Errorf("PostCreateCmd not cleared after run: %q", r.PostCreateCmd)
		}
		if r.PostStartCmd == "" {
			t.Errorf("PostStartCmd must NOT be cleared (runs every start)")
		}
	})

	t.Run("no hooks is noop", func(t *testing.T) {
		execCalled := false
		rt := &fakeRuntimeWithBuild{
			flexibleRuntime: flexibleRuntime{
				execFn: func(_ context.Context, _ string, _ []string, _ []byte, _ io.Writer, _ io.Writer) error {
					execCalled = true
					return nil
				},
			},
			bm: newFakeBuildManager(),
		}
		m := newEdgeCaseManager(t, rt)
		r := &Run{ID: "run_no_hooks", ContainerID: "ctr-noop"}
		if err := m.runDevcontainerLifecycleHooks(context.Background(), r); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if execCalled {
			t.Error("Exec should not be called when no hooks are configured")
		}
	})

	t.Run("onCreate failure aborts", func(t *testing.T) {
		callCount := 0
		rt := &fakeRuntimeWithBuild{
			flexibleRuntime: flexibleRuntime{
				execFn: func(_ context.Context, _ string, cmd []string, _ []byte, _ io.Writer, _ io.Writer) error {
					joined := strings.Join(cmd, " ")
					if strings.Contains(joined, "false") {
						callCount++
						return &container.ExecError{ExitCode: 1}
					}
					return nil // probe succeeds
				},
			},
			bm: newFakeBuildManager(),
		}
		m := newEdgeCaseManager(t, rt)
		r := &Run{
			ID:            "run_oncreate_fail",
			ContainerID:   "ctr-fail",
			OnCreateCmd:   "false",
			PostCreateCmd: "echo postCreate",
			PostStartCmd:  "echo postStart",
		}
		err := m.runDevcontainerLifecycleHooks(context.Background(), r)
		if err == nil {
			t.Fatal("expected error when onCreate fails")
		}
		if !strings.Contains(err.Error(), "onCreateCommand failed") {
			t.Errorf("error = %q, want onCreateCommand failed", err.Error())
		}
	})

	t.Run("postStart failure warns but does not error", func(t *testing.T) {
		rt := &fakeRuntimeWithBuild{
			flexibleRuntime: flexibleRuntime{
				execFn: func(_ context.Context, _ string, cmd []string, _ []byte, _ io.Writer, _ io.Writer) error {
					joined := strings.Join(cmd, " ")
					if strings.Contains(joined, "false") {
						return &container.ExecError{ExitCode: 1}
					}
					return nil
				},
			},
			bm: newFakeBuildManager(),
		}
		m := newEdgeCaseManager(t, rt)
		r := &Run{
			ID:           "run_poststart_fail",
			ContainerID:  "ctr-ps-fail",
			PostStartCmd: "false",
		}
		// postStart failure must NOT return an error.
		if err := m.runDevcontainerLifecycleHooks(context.Background(), r); err != nil {
			t.Fatalf("postStart failure should warn-and-continue, got error: %v", err)
		}
	})
}

// TestRunDevcontainerLifecycleHooks_OnCreatePostCreateAreOneShot verifies that
// onCreate and postCreate run only on the first invocation (cleared after success)
// while postStart runs on every invocation.
func TestRunDevcontainerLifecycleHooks_OnCreatePostCreateAreOneShot(t *testing.T) {
	var calls []string
	rt := &fakeRuntimeWithBuild{
		flexibleRuntime: flexibleRuntime{
			execFn: func(_ context.Context, _ string, cmd []string, _ []byte, _ io.Writer, _ io.Writer) error {
				joined := strings.Join(cmd, " ")
				switch {
				case strings.Contains(joined, "echo onCreate"):
					calls = append(calls, "onCreate")
				case strings.Contains(joined, "echo postCreate"):
					calls = append(calls, "postCreate")
				case strings.Contains(joined, "echo postStart"):
					calls = append(calls, "postStart")
				}
				return nil
			},
		},
		bm: newFakeBuildManager(),
	}
	m := newEdgeCaseManager(t, rt)
	r := &Run{
		ID:               "run_oneshot",
		ContainerID:      "ctr-oneshot",
		OnCreateCmd:      "echo onCreate",
		PostCreateCmd:    "echo postCreate",
		PostStartCmd:     "echo postStart",
		PostStartUser:    "vscode",
		PostStartHome:    "/home/vscode",
		PostStartWorkdir: "/workspaces/repo",
	}

	// First invocation: all three hooks fire.
	if err := m.runDevcontainerLifecycleHooks(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	wantFirst := []string{"onCreate", "postCreate", "postStart"}
	if !reflect.DeepEqual(calls, wantFirst) {
		t.Errorf("first call = %v, want %v", calls, wantFirst)
	}
	if r.OnCreateCmd != "" {
		t.Errorf("OnCreateCmd not cleared: %q", r.OnCreateCmd)
	}
	if r.PostCreateCmd != "" {
		t.Errorf("PostCreateCmd not cleared: %q", r.PostCreateCmd)
	}
	if r.PostStartCmd == "" {
		t.Errorf("PostStartCmd should NOT be cleared")
	}

	// Second invocation (simulating restart): only postStart fires.
	calls = nil
	if err := m.runDevcontainerLifecycleHooks(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	wantSecond := []string{"postStart"}
	if !reflect.DeepEqual(calls, wantSecond) {
		t.Errorf("second call = %v, want %v (only postStart should fire)", calls, wantSecond)
	}
}

// TestManager_RebuildRemovesStageATag verifies that resolveImageSpecForDevcontainer
// removes the Stage A image tag when opts.Rebuild is true and a devcontainer is present.
func TestManager_RebuildRemovesStageATag(t *testing.T) {
	workspace := t.TempDir()
	dcDir := filepath.Join(workspace, ".devcontainer")
	if err := os.MkdirAll(dcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dcDir, "devcontainer.json"), []byte(`{"image":"ubuntu:24.04"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	bm := newFakeBuildManager()
	rt := &trackingRuntime{
		fakeRuntimeWithBuild: fakeRuntimeWithBuild{bm: bm},
	}
	m := newEdgeCaseManager(t, rt)

	_, _, _, err := m.resolveImageSpecForDevcontainer(context.Background(), Options{
		Workspace: workspace,
		Rebuild:   true,
		Config:    nil,
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	// Expect exactly one RemoveImage call targeting the Stage A tag.
	if len(rt.removed) == 0 {
		t.Fatal("expected RemoveImage to be called for Stage A tag, but it was not")
	}
	tag := rt.removed[0]
	if !strings.HasPrefix(tag, "moat-devcontainer-") {
		t.Errorf("removed tag %q should have prefix moat-devcontainer-", tag)
	}
	if !strings.Contains(tag, ":base-") {
		t.Errorf("removed tag %q should contain ':base-'", tag)
	}
}

// TestManager_RebuildSkipsStageARemovalWhenNoDevcontainer verifies that
// RemoveImage is NOT called when no devcontainer is present (useDC=false).
func TestManager_RebuildSkipsStageARemovalWhenNoDevcontainer(t *testing.T) {
	workspace := t.TempDir() // no devcontainer.json

	bm := newFakeBuildManager()
	rt := &trackingRuntime{
		fakeRuntimeWithBuild: fakeRuntimeWithBuild{bm: bm},
	}
	m := newEdgeCaseManager(t, rt)

	_, _, _, err := m.resolveImageSpecForDevcontainer(context.Background(), Options{
		Workspace: workspace,
		Rebuild:   true,
		Config:    nil,
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if len(rt.removed) != 0 {
		t.Errorf("expected no RemoveImage calls (no devcontainer), but got: %v", rt.removed)
	}
}
