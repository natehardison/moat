package devcontainer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParse_Build(t *testing.T) {
	dir := setupWorkspace(t, "with-build.json")
	cfg, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if cfg.Build == nil {
		t.Fatal("Build is nil")
	}
	if cfg.Build.Dockerfile != "Dockerfile" {
		t.Errorf("Dockerfile = %q", cfg.Build.Dockerfile)
	}
	if cfg.Build.Context != ".." {
		t.Errorf("Context = %q", cfg.Build.Context)
	}
	if cfg.Build.Args["BASE"] != "ubuntu:24.04" {
		t.Errorf("Args[BASE] = %q", cfg.Build.Args["BASE"])
	}
	if cfg.Build.Target != "dev" {
		t.Errorf("Target = %q", cfg.Build.Target)
	}
	if cfg.User != "vscode" {
		t.Errorf("User = %q", cfg.User)
	}
	if cfg.Home != "/home/vscode" {
		t.Errorf("Home = %q", cfg.Home)
	}
}

func TestParse_UserPrecedence(t *testing.T) {
	// remoteUser wins over containerUser
	dir := setupWorkspace(t, "users.json")
	cfg, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if cfg.User != "vscode" {
		t.Errorf("User = %q, want vscode (remoteUser wins over containerUser)", cfg.User)
	}
}

func TestParse_NoImageNoBuild(t *testing.T) {
	dir := t.TempDir()
	dcDir := filepath.Join(dir, ".devcontainer")
	os.MkdirAll(dcDir, 0o755)
	os.WriteFile(filepath.Join(dcDir, "devcontainer.json"), []byte(`{"name": "broken"}`), 0o644)
	_, err := Detect(dir)
	if err == nil {
		t.Fatal("Detect should fail when neither image nor build is set")
	}
}

func TestParse_BrokenJSON(t *testing.T) {
	dir := t.TempDir()
	dcDir := filepath.Join(dir, ".devcontainer")
	os.MkdirAll(dcDir, 0o755)
	os.WriteFile(filepath.Join(dcDir, "devcontainer.json"), []byte(`{not json`), 0o644)
	_, err := Detect(dir)
	if err == nil {
		t.Fatal("Detect should fail on malformed JSON")
	}
}

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

func TestExpandVars(t *testing.T) {
	t.Setenv("USER", "alice")
	workspace := "/home/alice/repo"
	cenv := map[string]string{"FOO": "bar"}
	ctx := expandContext{
		workspace:       workspace,
		workspaceFolder: "/workspaces/repo",
		containerEnv:    cenv,
	}
	cases := []struct{ in, want string }{
		{"${localWorkspaceFolder}", "/home/alice/repo"},
		{"${localWorkspaceFolderBasename}", "repo"},
		{"${containerWorkspaceFolder}", "/workspaces/repo"},
		{"${containerWorkspaceFolderBasename}", "repo"},
		{"${localEnv:USER}", "alice"},
		{"${localEnv:NOPE:fallback}", "fallback"},
		{"${localEnv:NOPE}", ""},
		{"${containerEnv:FOO}", "bar"},
		{"${containerEnv:MISSING:dflt}", "dflt"},
		{"prefix-${localEnv:USER}-suffix", "prefix-alice-suffix"},
		{"${unknownVar}", "${unknownVar}"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got := expandVars(c.in, ctx)
			if got != c.want {
				t.Errorf("expandVars(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestParse_EnvAndWorkspaceFolder(t *testing.T) {
	t.Setenv("USER", "alice")
	dir := setupWorkspace(t, "env-and-folder.json")
	cfg, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	base := filepath.Base(dir)
	wantFolder := "/work/" + base
	if cfg.WorkspaceFolder != wantFolder {
		t.Errorf("WorkspaceFolder = %q, want %q", cfg.WorkspaceFolder, wantFolder)
	}
	if cfg.ContainerEnv["BASE"] != "from-container" {
		t.Errorf("containerEnv[BASE] = %q", cfg.ContainerEnv["BASE"])
	}
	if cfg.ContainerEnv["LOCAL_USER"] != "alice" {
		t.Errorf("containerEnv[LOCAL_USER] = %q, want alice", cfg.ContainerEnv["LOCAL_USER"])
	}
	if cfg.RemoteEnv["DERIVED"] != "from-container-x" {
		t.Errorf("remoteEnv[DERIVED] = %q, want from-container-x", cfg.RemoteEnv["DERIVED"])
	}
}

func TestParse_Mounts(t *testing.T) {
	dir := setupWorkspace(t, "mounts.json")
	cfg, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(cfg.Mounts) != 3 {
		t.Fatalf("len(Mounts) = %d, want 3", len(cfg.Mounts))
	}
	m0 := cfg.Mounts[0]
	if m0.Source != filepath.Join(dir, "cache") || m0.Target != "/cache" || m0.Type != "bind" || m0.ReadOnly {
		t.Errorf("Mount[0] = %+v", m0)
	}
	m1 := cfg.Mounts[1]
	if m1.Source != "named-vol" || m1.Target != "/data" || m1.Type != "volume" {
		t.Errorf("Mount[1] = %+v", m1)
	}
	m2 := cfg.Mounts[2]
	if !m2.ReadOnly {
		t.Errorf("Mount[2].ReadOnly = false, want true")
	}
}

func TestParse_BadMountType(t *testing.T) {
	dir := t.TempDir()
	dcDir := filepath.Join(dir, ".devcontainer")
	os.MkdirAll(dcDir, 0o755)
	os.WriteFile(filepath.Join(dcDir, "devcontainer.json"), []byte(`{
      "image": "ubuntu:24.04",
      "mounts": ["source=x,target=y,type=tmpfs"]
    }`), 0o644)
	_, err := Detect(dir)
	if err == nil {
		t.Fatal("Detect should fail on unsupported mount type")
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
