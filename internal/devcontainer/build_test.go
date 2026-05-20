package devcontainer

import (
	"os"
	"path/filepath"
	"testing"
)

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
