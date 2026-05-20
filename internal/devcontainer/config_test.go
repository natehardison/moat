package devcontainer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetect_Missing(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect(missing) returned err: %v", err)
	}
	if cfg != nil {
		t.Errorf("Detect(missing) = %+v, want nil", cfg)
	}
}

func TestDetect_Minimal(t *testing.T) {
	dir := setupWorkspace(t, "minimal-image.json")
	cfg, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if cfg == nil {
		t.Fatal("Detect returned nil")
	}
	if cfg.Image != "ubuntu:24.04" {
		t.Errorf("Image = %q, want ubuntu:24.04", cfg.Image)
	}
	if cfg.User != "root" {
		t.Errorf("User = %q, want root", cfg.User)
	}
	if cfg.Home != "/root" {
		t.Errorf("Home = %q, want /root", cfg.Home)
	}
}

func TestStripJSONC(t *testing.T) {
	cases := []struct {
		name string
		in   string
		out  string
	}{
		{"plain", `{"a":1}`, `{"a":1}`},
		{"line-comment", "{\n  // comment\n  \"a\": 1\n}", "{\n  \n  \"a\": 1\n}"},
		{"block-comment", `{"a": /* hi */ 1}`, `{"a":  1}`},
		{"comment-in-string", `{"a": "// not a comment"}`, `{"a": "// not a comment"}`},
		{"escaped-quote-in-string", `{"a": "x\"// still string"}`, `{"a": "x\"// still string"}`},
		{"trailing-comma-object", `{"a":1,}`, `{"a":1}`},
		{"trailing-comma-array", `{"a":[1,2,]}`, `{"a":[1,2]}`},
		{"unterminated-block", `{"a": /* unclosed`, `{"a": `},
		{"block-comment-at-end", `{"a": /* trailing */}`, `{"a": }`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := string(stripJSONC([]byte(c.in)))
			if got != c.out {
				t.Errorf("stripJSONC(%q) = %q, want %q", c.in, got, c.out)
			}
		})
	}
}

// setupWorkspace creates a temp dir containing .devcontainer/devcontainer.json
// copied from testdata/<fixture>.
func setupWorkspace(t *testing.T, fixture string) string {
	t.Helper()
	dir := t.TempDir()
	dcDir := filepath.Join(dir, ".devcontainer")
	if err := os.MkdirAll(dcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dcDir, "devcontainer.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}
