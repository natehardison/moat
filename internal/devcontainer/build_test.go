package devcontainer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/container"
)

// fakeBuildManager implements container.BuildManager for testing.
type fakeBuildManager struct {
	builds []fakeBuild
	exists map[string]bool
}

type fakeBuild struct {
	dockerfile string
	tag        string
}

func (f *fakeBuildManager) BuildImage(ctx context.Context, df, tag string, opts container.BuildOptions) error {
	f.builds = append(f.builds, fakeBuild{df, tag})
	if f.exists == nil {
		f.exists = map[string]bool{}
	}
	f.exists[tag] = true
	return nil
}

func (f *fakeBuildManager) ImageExists(ctx context.Context, tag string) (bool, error) {
	return f.exists[tag], nil
}

func (f *fakeBuildManager) GetImageHomeDir(ctx context.Context, image string) string { return "/root" }

func TestContentHash_Stable(t *testing.T) {
	src := filepath.Join("testdata", "hash-fixture")
	h1, err := ContentHash(src)
	if err != nil {
		t.Fatalf("ContentHash: %v", err)
	}
	if len(h1) != 64 {
		t.Errorf("hex len = %d, want 64", len(h1))
	}
	// Copy the .devcontainer dir to a different path and re-hash. The hash
	// must not depend on the workspace path.
	other := t.TempDir()
	copyTree(t, src, other)
	h2, err := ContentHash(other)
	if err != nil {
		t.Fatalf("ContentHash 2: %v", err)
	}
	if h1 != h2 {
		t.Errorf("hashes differ between paths: %s vs %s", h1, h2)
	}
}

func TestContentHash_ChangesWithContent(t *testing.T) {
	src := filepath.Join("testdata", "hash-fixture")
	h1, _ := ContentHash(src)
	other := t.TempDir()
	copyTree(t, src, other)
	if err := os.WriteFile(
		filepath.Join(other, ".devcontainer", "Dockerfile"),
		[]byte("FROM ubuntu:24.04\nRUN echo changed\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	h2, _ := ContentHash(other)
	if h1 == h2 {
		t.Error("hash did not change when Dockerfile changed")
	}
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	if err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	}); err != nil {
		t.Fatal(err)
	}
}

func TestBuildBase_ImagePulledViaFROM(t *testing.T) {
	dir := setupWorkspace(t, "minimal-image.json")
	cfg, _ := Detect(dir)
	bm := &fakeBuildManager{}
	tag, err := BuildBase(context.Background(), bm, dir, cfg, BuildOptions{})
	if err != nil {
		t.Fatalf("BuildBase: %v", err)
	}
	if !strings.HasPrefix(tag, "moat-devcontainer-") || !strings.Contains(tag, ":base-") {
		t.Errorf("tag = %q", tag)
	}
	if len(bm.builds) != 1 {
		t.Fatalf("got %d builds, want 1", len(bm.builds))
	}
	if !strings.Contains(bm.builds[0].dockerfile, "FROM ubuntu:24.04") {
		t.Errorf("dockerfile = %q", bm.builds[0].dockerfile)
	}
}

func TestBuildBase_DockerfileWithArgsAndTarget(t *testing.T) {
	dir := setupWorkspace(t, "with-build.json")
	// The fixture references "Dockerfile" relative to .devcontainer/ and
	// context "..". Materialize a stub Dockerfile so the loader doesn't fail.
	if err := os.WriteFile(
		filepath.Join(dir, ".devcontainer", "Dockerfile"),
		[]byte("ARG BASE=ubuntu:24.04\nFROM ${BASE} AS dev\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	cfg, err := Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	bm := &fakeBuildManager{}
	tag, err := BuildBase(context.Background(), bm, dir, cfg, BuildOptions{})
	if err != nil {
		t.Fatalf("BuildBase: %v", err)
	}
	if tag == "" {
		t.Fatal("empty tag")
	}
	if len(bm.builds) != 1 {
		t.Fatalf("got %d builds, want 1", len(bm.builds))
	}
	df := bm.builds[0].dockerfile
	if !strings.Contains(df, "ARG BASE=") {
		t.Errorf("dockerfile content not preserved: %q", df)
	}
}

func TestBuildBase_CachedSkipsBuild(t *testing.T) {
	dir := setupWorkspace(t, "minimal-image.json")
	cfg, _ := Detect(dir)
	bm := &fakeBuildManager{exists: map[string]bool{}}
	tag1, _ := BuildBase(context.Background(), bm, dir, cfg, BuildOptions{})
	// Mark cached
	bm.exists[tag1] = true
	bm.builds = nil
	tag2, err := BuildBase(context.Background(), bm, dir, cfg, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if tag1 != tag2 {
		t.Errorf("tags differ: %s vs %s", tag1, tag2)
	}
	if len(bm.builds) != 0 {
		t.Errorf("cached path should skip BuildImage, got %d builds", len(bm.builds))
	}
}

func TestBuildBase_ContainerEnvOverlay(t *testing.T) {
	dir := setupWorkspace(t, "env-and-folder.json")
	t.Setenv("USER", "alice")
	cfg, err := Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	bm := &fakeBuildManager{}
	tag, err := BuildBase(context.Background(), bm, dir, cfg, BuildOptions{})
	if err != nil {
		t.Fatalf("BuildBase: %v", err)
	}
	if len(bm.builds) != 2 {
		t.Fatalf("got %d builds, want 2 (base + env overlay)", len(bm.builds))
	}
	overlay := bm.builds[1].dockerfile
	if !strings.Contains(overlay, `ENV BASE="from-container"`) {
		t.Errorf("overlay missing BASE env: %q", overlay)
	}
	if !strings.Contains(overlay, `ENV LOCAL_USER="alice"`) {
		t.Errorf("overlay missing LOCAL_USER env: %q", overlay)
	}
	if bm.builds[1].tag != tag {
		t.Errorf("overlay tag = %q, want %q", bm.builds[1].tag, tag)
	}
}
