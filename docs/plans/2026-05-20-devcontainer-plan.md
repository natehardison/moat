# Devcontainer Support Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `.devcontainer/devcontainer.json` detection and image construction to moat so that workspaces with a devcontainer "just work" via `moat run`.

**Architecture:** New `internal/devcontainer/` package parses the JSONC file, runs `initializeCommand` on the host, and builds a content-addressed base image (Stage A). `internal/run/manager.go` then feeds that base tag into `ImageSpec.BaseImage` and the existing `internal/deps`-driven Dockerfile generates Stage B with moat's overlay, including a UID-remap layer for Linux. Container runtime calls honor `remoteUser`, `workspaceFolder`, `remoteEnv`, mounts, and the four devcontainer lifecycle hooks (`onCreate`/`postCreate`/`postStart`).

**Tech Stack:** Go 1.22+, standard library only (no new deps). Existing moat internals: `internal/container.Runtime`, `internal/deps.ImageSpec`/`GenerateDockerfile`, `internal/run.Manager`, `internal/log`, `internal/ui`.

**Spec:** [`docs/plans/2026-05-20-devcontainer-design.md`](2026-05-20-devcontainer-design.md)

---

## File map

| File | Purpose | New / Modified |
| --- | --- | --- |
| `internal/devcontainer/config.go` | `Config`, `Mount`, `BuildConfig` types; `Detect`, `parseJSONC`, `expandVars` | New |
| `internal/devcontainer/build.go` | `BuildBase`, content hash, `containerEnv` overlay | New |
| `internal/devcontainer/hooks.go` | `RunHook`, `RunInitializeCommand`, command shape parsing | New |
| `internal/devcontainer/probe.go` | `ProbeUserEnv` for login-shell env extraction | New |
| `internal/devcontainer/config_test.go` | Parser + var expansion + validation tests | New |
| `internal/devcontainer/build_test.go` | Build pipeline tests with fake runtime | New |
| `internal/devcontainer/hooks_test.go` | Hook execution tests with fake runtime | New |
| `internal/devcontainer/probe_test.go` | Env probe parsing tests | New |
| `internal/devcontainer/testdata/*.json` | Fixture devcontainer.json files | New |
| `internal/deps/imagespec.go` | Add `RemapUser`/`RemapUID`/`RemapGID` fields, include in hash | Modified |
| `internal/deps/dockerfile.go` | Emit UID-remap RUN block when `RemapUser != ""` | Modified |
| `internal/run/manager.go` | Detect devcontainer, build Stage A, plumb base tag into `ImageSpec`, override user/workdir/mounts/env | Modified |
| `internal/run/run.go` (or wherever `Run` struct lives) | Add `DevcontainerHash`, lifecycle hook fields | Modified |
| `internal/run/devcontainer_integration_test.go` | End-to-end manager test with fake runtime | New |
| `cmd/moat/cli/run.go` | `--no-devcontainer` flag | Modified |
| `cmd/moat/cli/init.go` | Detect devcontainer, write minimal moat.yaml | Modified |
| `cmd/moat/cli/doctor.go` | "Devcontainer" diagnostic section | Modified |
| `cmd/moat/cli/status.go` (or `list.go`) | Show `DevcontainerHash` drift hint | Modified |
| `internal/e2e/devcontainer_test.go` | `image-only`, `dockerfile-build`, `full-lifecycle` E2E | New |
| `internal/e2e/testdata/devcontainer/*` | E2E fixture workspaces | New |
| `docs/content/reference/01-cli.md` | `--no-devcontainer`, `--rebuild` updates | Modified |
| `docs/content/reference/02-moat-yaml.md` | Note `base_image`/`dependencies` precedence over devcontainer | Modified |
| `docs/content/guides/devcontainer.md` | New "Using devcontainer.json with moat" guide | New |
| `CHANGELOG.md` | `Added: devcontainer.json support` entry | Modified |

---

## PR 0 — Manager refactor (optional prep)

This PR extracts a single helper so PR 2's diff stays tight. Skip if reviewers prefer to land everything in PR 2.

### Task 0.1: Extract `resolveBaseImage` helper from `manager.go`

**Files:**
- Modify: `internal/run/manager.go` (around line 1742-1745)

- [ ] **Step 1: Read the current inline assignment**

Open `internal/run/manager.go` and locate:
```go
var baseImage string
if opts.Config != nil {
    baseImage = opts.Config.BaseImage
}
imageSpec := &deps.ImageSpec{
    BaseImage:          baseImage,
    ...
```

- [ ] **Step 2: Write a unit test for the helper**

Create or extend `internal/run/manager_test.go` with:

```go
func TestResolveBaseImage_NoConfig(t *testing.T) {
    got := resolveBaseImage(nil)
    if got != "" {
        t.Errorf("resolveBaseImage(nil) = %q, want \"\"", got)
    }
}

func TestResolveBaseImage_FromConfig(t *testing.T) {
    cfg := &config.Config{BaseImage: "ubuntu:24.04"}
    got := resolveBaseImage(cfg)
    if got != "ubuntu:24.04" {
        t.Errorf("resolveBaseImage = %q, want ubuntu:24.04", got)
    }
}
```

- [ ] **Step 3: Run the tests and confirm they fail**

Run: `go test ./internal/run/ -run TestResolveBaseImage -v`
Expected: FAIL (function undefined).

- [ ] **Step 4: Add the helper**

In `internal/run/manager.go`, just before `func (m *Manager) Create(...)`:

```go
// resolveBaseImage returns the explicit base image from moat.yaml, or "".
// Empty means "let downstream code decide" (auto-select or devcontainer-supplied).
func resolveBaseImage(cfg *config.Config) string {
    if cfg == nil {
        return ""
    }
    return cfg.BaseImage
}
```

Replace the inline block at the `imageSpec := &deps.ImageSpec{` site with `BaseImage: resolveBaseImage(opts.Config),`.

- [ ] **Step 5: Run all tests in `internal/run`**

Run: `make test-unit ARGS='-run TestResolveBaseImage'`
Expected: PASS.

Then: `make test-unit`
Expected: PASS (no regressions).

- [ ] **Step 6: Commit**

```bash
git add internal/run/manager.go internal/run/manager_test.go
git commit -m "refactor(run): extract resolveBaseImage helper"
```

---

## PR 1 — `internal/devcontainer/` package

All tasks here produce **dead code**: nothing imports the package yet. The CLI flow is unchanged. This PR ships only when its unit tests pass on CI.

### Task 1.1: Create package skeleton + Detect()

**Files:**
- Create: `internal/devcontainer/config.go`
- Create: `internal/devcontainer/config_test.go`
- Create: `internal/devcontainer/testdata/minimal-image.json`

- [ ] **Step 1: Write the failing test**

Create `internal/devcontainer/config_test.go`:

```go
package devcontainer

import (
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
```

Add the missing import: `"os"`.

Create `internal/devcontainer/testdata/minimal-image.json`:

```json
{
  "image": "ubuntu:24.04"
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/devcontainer/ -run TestDetect -v`
Expected: FAIL (package or `Detect` undefined).

- [ ] **Step 3: Create `config.go` skeleton with Config type and Detect**

```go
// Package devcontainer parses and acts on devcontainer.json (the VS Code
// Dev Containers spec) so moat can use a workspace's devcontainer as the
// source of truth for image, user, mounts, env, and lifecycle hooks.
package devcontainer

import (
    "errors"
    "fmt"
    "os"
    "path/filepath"
)

// Config is a parsed devcontainer.json, normalized for moat's use.
type Config struct {
    Image               string
    Build               *BuildConfig
    User                string
    Home                string
    WorkspaceFolder     string
    ContainerEnv        map[string]string
    RemoteEnv           map[string]string
    Mounts              []Mount
    InitializeCmd       string
    OnCreateCmd         string
    PostCreateCmd       string
    PostStartCmd        string
    SourcePath          string
    UpdateRemoteUserUID bool
}

// BuildConfig is the "build" subobject from devcontainer.json.
type BuildConfig struct {
    Dockerfile string            // path relative to .devcontainer/
    Context    string            // path relative to .devcontainer/; default "."
    Args       map[string]string // --build-arg key=value
    Target     string            // --target
}

// Mount is a single bind or volume mount declared in devcontainer.json.
type Mount struct {
    Source   string
    Target   string
    Type     string // "bind" or "volume"
    ReadOnly bool
}

// ErrNotFound is returned by Detect when no devcontainer.json exists.
// Callers should not treat this as an error; Detect returns (nil, nil) instead.
var ErrNotFound = errors.New("devcontainer.json not found")

// Detect returns the parsed devcontainer.json from <workspace>/.devcontainer/,
// or (nil, nil) if the file does not exist. A malformed file is a hard error.
func Detect(workspace string) (*Config, error) {
    path := filepath.Join(workspace, ".devcontainer", "devcontainer.json")
    raw, err := os.ReadFile(path)
    if err != nil {
        if os.IsNotExist(err) {
            return nil, nil
        }
        return nil, fmt.Errorf("read %s: %w", path, err)
    }
    return parse(path, workspace, raw)
}

// parse is the testable core of Detect.
func parse(path, workspace string, raw []byte) (*Config, error) {
    // Stub — wired up in Task 1.3.
    _ = workspace
    return &Config{
        Image:               "ubuntu:24.04",
        User:                "root",
        Home:                "/root",
        UpdateRemoteUserUID: true,
        SourcePath:          path,
    }, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/devcontainer/ -run TestDetect -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/devcontainer/config.go internal/devcontainer/config_test.go internal/devcontainer/testdata/minimal-image.json
git commit -m "feat(devcontainer): add package skeleton and Detect()"
```

### Task 1.2: JSONC stripping

**Files:**
- Modify: `internal/devcontainer/config.go`
- Modify: `internal/devcontainer/config_test.go`

- [ ] **Step 1: Write failing tests for `stripJSONC`**

Append to `config_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/devcontainer/ -run TestStripJSONC -v`
Expected: FAIL (`stripJSONC` undefined).

- [ ] **Step 3: Implement `stripJSONC`**

Add to `config.go`:

```go
// stripJSONC removes // line comments, /* block comments */, and trailing
// commas from JSONC, leaving the result valid JSON. String literals are
// preserved verbatim, including escape sequences.
func stripJSONC(in []byte) []byte {
    out := make([]byte, 0, len(in))
    i := 0
    inString := false
    for i < len(in) {
        c := in[i]
        if inString {
            out = append(out, c)
            if c == '\\' && i+1 < len(in) {
                out = append(out, in[i+1])
                i += 2
                continue
            }
            if c == '"' {
                inString = false
            }
            i++
            continue
        }
        if c == '"' {
            inString = true
            out = append(out, c)
            i++
            continue
        }
        if c == '/' && i+1 < len(in) {
            if in[i+1] == '/' {
                for i < len(in) && in[i] != '\n' {
                    i++
                }
                continue
            }
            if in[i+1] == '*' {
                i += 2
                for i+1 < len(in) && !(in[i] == '*' && in[i+1] == '/') {
                    i++
                }
                if i+1 < len(in) {
                    i += 2
                }
                continue
            }
        }
        // Drop a trailing comma before } or ] (skipping whitespace).
        if c == ',' {
            j := i + 1
            for j < len(in) && (in[j] == ' ' || in[j] == '\t' || in[j] == '\n' || in[j] == '\r') {
                j++
            }
            if j < len(in) && (in[j] == '}' || in[j] == ']') {
                i++
                continue
            }
        }
        out = append(out, c)
        i++
    }
    return out
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/devcontainer/ -run TestStripJSONC -v`
Expected: PASS for all subtests.

- [ ] **Step 5: Commit**

```bash
git add internal/devcontainer/config.go internal/devcontainer/config_test.go
git commit -m "feat(devcontainer): JSONC stripping (comments, trailing commas)"
```

### Task 1.3: Real parser wired into Detect()

**Files:**
- Modify: `internal/devcontainer/config.go`
- Modify: `internal/devcontainer/config_test.go`
- Create: `internal/devcontainer/testdata/with-build.json`
- Create: `internal/devcontainer/testdata/users.json`

- [ ] **Step 1: Add fixture files**

`internal/devcontainer/testdata/with-build.json`:

```json
{
  "build": {
    "dockerfile": "Dockerfile",
    "context": "..",
    "args": { "BASE": "ubuntu:24.04" },
    "target": "dev"
  },
  "remoteUser": "vscode"
}
```

`internal/devcontainer/testdata/users.json`:

```json
{
  "image": "ubuntu:24.04",
  "containerUser": "node",
  "remoteUser": "vscode"
}
```

- [ ] **Step 2: Write failing tests**

Append to `config_test.go`:

```go
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
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/devcontainer/ -run TestParse -v`
Expected: FAIL.

- [ ] **Step 4: Replace the stub parse() with the real implementation**

Replace the existing `parse` in `config.go` with:

```go
import (
    "encoding/json"
    // ... existing imports
)

func parse(path, workspace string, raw []byte) (*Config, error) {
    var top map[string]any
    if err := json.Unmarshal(stripJSONC(raw), &top); err != nil {
        return nil, fmt.Errorf("parse %s: %w", path, err)
    }

    cfg := &Config{
        SourcePath:          path,
        UpdateRemoteUserUID: true,
        ContainerEnv:        map[string]string{},
        RemoteEnv:           map[string]string{},
    }

    if v, ok := top["image"].(string); ok {
        cfg.Image = v
    }
    if rawBuild, ok := top["build"].(map[string]any); ok {
        cfg.Build = parseBuild(rawBuild)
    }
    if cfg.Image == "" && cfg.Build == nil {
        return nil, fmt.Errorf("%s: must specify either \"image\" or \"build.dockerfile\"", path)
    }

    // User: remoteUser ?? containerUser ?? "root"
    if v, ok := top["remoteUser"].(string); ok && v != "" {
        cfg.User = v
    } else if v, ok := top["containerUser"].(string); ok && v != "" {
        cfg.User = v
    } else {
        cfg.User = "root"
    }
    if cfg.User == "root" {
        cfg.Home = "/root"
    } else {
        cfg.Home = "/home/" + cfg.User
    }

    if v, ok := top["updateRemoteUserUID"].(bool); ok {
        cfg.UpdateRemoteUserUID = v
    }

    return cfg, nil
}

func parseBuild(raw map[string]any) *BuildConfig {
    df, _ := raw["dockerfile"].(string)
    if df == "" {
        return nil
    }
    bc := &BuildConfig{Dockerfile: df, Context: "."}
    if v, ok := raw["context"].(string); ok && v != "" {
        bc.Context = v
    }
    if v, ok := raw["target"].(string); ok {
        bc.Target = v
    }
    if rawArgs, ok := raw["args"].(map[string]any); ok && len(rawArgs) > 0 {
        bc.Args = make(map[string]string, len(rawArgs))
        for k, v := range rawArgs {
            bc.Args[k] = fmt.Sprint(v)
        }
    }
    return bc
}
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/devcontainer/ -v`
Expected: PASS for all tests.

- [ ] **Step 6: Commit**

```bash
git add internal/devcontainer/config.go internal/devcontainer/config_test.go internal/devcontainer/testdata/
git commit -m "feat(devcontainer): parse image, build, remoteUser/containerUser"
```

### Task 1.4: Variable expansion

**Files:**
- Modify: `internal/devcontainer/config.go`
- Modify: `internal/devcontainer/config_test.go`

- [ ] **Step 1: Write failing tests**

Append to `config_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/devcontainer/ -run TestExpandVars -v`
Expected: FAIL.

- [ ] **Step 3: Implement expansion**

Add to `config.go`:

```go
import (
    "regexp"
    // ...
)

type expandContext struct {
    workspace       string
    workspaceFolder string
    containerEnv    map[string]string // optional; if non-nil, ${containerEnv:X} is resolved
}

var varRe = regexp.MustCompile(`\$\{([^}]+)\}`)

func expandVars(s string, ctx expandContext) string {
    return varRe.ReplaceAllStringFunc(s, func(m string) string {
        name := m[2 : len(m)-1]
        switch name {
        case "localWorkspaceFolder":
            return ctx.workspace
        case "localWorkspaceFolderBasename":
            return filepath.Base(ctx.workspace)
        case "containerWorkspaceFolder":
            if ctx.workspaceFolder != "" {
                return ctx.workspaceFolder
            }
            return "/workspaces/" + filepath.Base(ctx.workspace)
        case "containerWorkspaceFolderBasename":
            if ctx.workspaceFolder != "" {
                return filepath.Base(ctx.workspaceFolder)
            }
            return filepath.Base(ctx.workspace)
        }
        if strings.HasPrefix(name, "localEnv:") {
            return lookupWithDefault(name[len("localEnv:"):], os.Getenv)
        }
        if strings.HasPrefix(name, "containerEnv:") && ctx.containerEnv != nil {
            return lookupWithDefault(name[len("containerEnv:"):], func(k string) string {
                return ctx.containerEnv[k]
            })
        }
        return m
    })
}

func lookupWithDefault(spec string, lookup func(string) string) string {
    name, dflt, hasDflt := strings.Cut(spec, ":")
    v := lookup(name)
    if v == "" && hasDflt {
        return dflt
    }
    return v
}
```

Add the `"strings"` import.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/devcontainer/ -run TestExpandVars -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/devcontainer/config.go internal/devcontainer/config_test.go
git commit -m "feat(devcontainer): variable expansion (localEnv, containerEnv, workspace)"
```

### Task 1.5: workspaceFolder, containerEnv, remoteEnv parsing

**Files:**
- Modify: `internal/devcontainer/config.go`
- Modify: `internal/devcontainer/config_test.go`
- Create: `internal/devcontainer/testdata/env-and-folder.json`

- [ ] **Step 1: Add fixture**

`internal/devcontainer/testdata/env-and-folder.json`:

```json
{
  "image": "ubuntu:24.04",
  "remoteUser": "dev",
  "workspaceFolder": "/work/${localWorkspaceFolderBasename}",
  "containerEnv": {
    "BASE": "from-container",
    "LOCAL_USER": "${localEnv:USER}"
  },
  "remoteEnv": {
    "DERIVED": "${containerEnv:BASE}-x"
  }
}
```

- [ ] **Step 2: Write failing test**

Append to `config_test.go`:

```go
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
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/devcontainer/ -run TestParse_EnvAndWorkspaceFolder -v`
Expected: FAIL.

- [ ] **Step 4: Extend parse()**

In `parse()`, after the user resolution block, before `return cfg, nil`:

```go
ctx := expandContext{workspace: workspace}
if v, ok := top["workspaceFolder"].(string); ok && v != "" {
    cfg.WorkspaceFolder = expandVars(v, ctx)
}
ctx.workspaceFolder = cfg.WorkspaceFolder

if rawCE, ok := top["containerEnv"].(map[string]any); ok {
    for k, v := range rawCE {
        cfg.ContainerEnv[k] = expandVars(fmt.Sprint(v), ctx)
    }
}
ctx.containerEnv = cfg.ContainerEnv

if rawRE, ok := top["remoteEnv"].(map[string]any); ok {
    for k, v := range rawRE {
        cfg.RemoteEnv[k] = expandVars(fmt.Sprint(v), ctx)
    }
}
```

- [ ] **Step 5: Run test**

Run: `go test ./internal/devcontainer/ -run TestParse_EnvAndWorkspaceFolder -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/devcontainer/config.go internal/devcontainer/config_test.go internal/devcontainer/testdata/env-and-folder.json
git commit -m "feat(devcontainer): parse workspaceFolder, containerEnv, remoteEnv"
```

### Task 1.6: Mounts parsing

**Files:**
- Modify: `internal/devcontainer/config.go`
- Modify: `internal/devcontainer/config_test.go`
- Create: `internal/devcontainer/testdata/mounts.json`

- [ ] **Step 1: Add fixture**

`internal/devcontainer/testdata/mounts.json`:

```json
{
  "image": "ubuntu:24.04",
  "mounts": [
    "source=${localWorkspaceFolder}/cache,target=/cache,type=bind",
    { "source": "named-vol", "target": "/data", "type": "volume" },
    "source=/tmp/ro,target=/ro,type=bind,readonly"
  ]
}
```

- [ ] **Step 2: Write failing tests**

Append to `config_test.go`:

```go
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
```

- [ ] **Step 3: Run tests to verify failure**

Run: `go test ./internal/devcontainer/ -run TestParse_Mount -v`
Expected: FAIL.

- [ ] **Step 4: Add Mounts parsing to parse()**

After the env block in `parse()`:

```go
if rawMounts, ok := top["mounts"].([]any); ok {
    for _, raw := range rawMounts {
        m, err := parseMount(raw, ctx)
        if err != nil {
            return nil, fmt.Errorf("%s: %w", path, err)
        }
        cfg.Mounts = append(cfg.Mounts, m)
    }
}
```

Add helper:

```go
func parseMount(raw any, ctx expandContext) (Mount, error) {
    var fields map[string]string
    switch v := raw.(type) {
    case string:
        fields = map[string]string{}
        for _, part := range strings.Split(v, ",") {
            kv := strings.SplitN(part, "=", 2)
            if len(kv) == 2 {
                fields[kv[0]] = kv[1]
            } else if kv[0] == "readonly" || kv[0] == "ro" {
                fields["readonly"] = "true"
            }
        }
    case map[string]any:
        fields = map[string]string{}
        for k, val := range v {
            fields[k] = fmt.Sprint(val)
        }
    default:
        return Mount{}, fmt.Errorf("unrecognized mount: %v", raw)
    }
    typ := fields["type"]
    if typ == "" {
        typ = "bind"
    }
    if typ != "bind" && typ != "volume" {
        return Mount{}, fmt.Errorf("unsupported mount type %q (only bind and volume)", typ)
    }
    ro := false
    if v, ok := fields["readonly"]; ok && (v == "true" || v == "1") {
        ro = true
    }
    return Mount{
        Source:   expandVars(fields["source"], ctx),
        Target:   expandVars(fields["target"], ctx),
        Type:     typ,
        ReadOnly: ro,
    }, nil
}
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/devcontainer/ -run TestParse_Mount -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/devcontainer/config.go internal/devcontainer/config_test.go internal/devcontainer/testdata/mounts.json
git commit -m "feat(devcontainer): parse mounts (string and object forms)"
```

### Task 1.7: Lifecycle commands (string, array, object shapes)

**Files:**
- Create: `internal/devcontainer/hooks.go`
- Create: `internal/devcontainer/hooks_test.go`
- Modify: `internal/devcontainer/config.go`
- Modify: `internal/devcontainer/config_test.go`
- Create: `internal/devcontainer/testdata/lifecycle.json`

- [ ] **Step 1: Add fixture**

`internal/devcontainer/testdata/lifecycle.json`:

```json
{
  "image": "ubuntu:24.04",
  "initializeCommand": "echo init",
  "onCreateCommand": ["echo", "hello"],
  "postCreateCommand": {
    "first":  "echo first",
    "second": ["echo", "second"]
  },
  "postStartCommand": "echo start"
}
```

- [ ] **Step 2: Write the parse-lifecycle test**

Create `internal/devcontainer/hooks_test.go`:

```go
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
```

And in `config_test.go`:

```go
func TestParse_Lifecycle(t *testing.T) {
    dir := setupWorkspace(t, "lifecycle.json")
    cfg, err := Detect(dir)
    if err != nil {
        t.Fatalf("Detect: %v", err)
    }
    if cfg.InitializeCmd != "echo init" {
        t.Errorf("InitializeCmd = %q", cfg.InitializeCmd)
    }
    if cfg.OnCreateCmd != "echo hello" {
        t.Errorf("OnCreateCmd = %q", cfg.OnCreateCmd)
    }
    if !strings.Contains(cfg.PostCreateCmd, "echo first") || !strings.Contains(cfg.PostCreateCmd, "echo second") {
        t.Errorf("PostCreateCmd = %q", cfg.PostCreateCmd)
    }
    if cfg.PostStartCmd != "echo start" {
        t.Errorf("PostStartCmd = %q", cfg.PostStartCmd)
    }
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/devcontainer/ -v`
Expected: FAIL (`parseLifecycleCommand` undefined).

- [ ] **Step 4: Implement parseLifecycleCommand**

Create `internal/devcontainer/hooks.go`:

```go
package devcontainer

import (
    "fmt"
    "strings"
)

// parseLifecycleCommand normalizes the three shapes the devcontainer spec
// allows — string, array, object — into a single shell-ready string.
// Returns "" if the input is nil or an unrecognized shape.
func parseLifecycleCommand(raw any) string {
    switch v := raw.(type) {
    case nil:
        return ""
    case string:
        return v
    case []any:
        parts := make([]string, 0, len(v))
        for _, x := range v {
            parts = append(parts, fmt.Sprint(x))
        }
        return strings.Join(parts, " ")
    case map[string]any:
        cmds := make([]string, 0, len(v))
        for _, val := range v {
            cmds = append(cmds, parseLifecycleCommand(val))
        }
        return strings.Join(cmds, " && ")
    default:
        return ""
    }
}
```

Add to `parse()` in `config.go` (after the mounts block):

```go
cfg.InitializeCmd = expandVars(parseLifecycleCommand(top["initializeCommand"]), ctx)
cfg.OnCreateCmd = expandVars(parseLifecycleCommand(top["onCreateCommand"]), ctx)
cfg.PostCreateCmd = expandVars(parseLifecycleCommand(top["postCreateCommand"]), ctx)
cfg.PostStartCmd = expandVars(parseLifecycleCommand(top["postStartCommand"]), ctx)
```

- [ ] **Step 5: Run all package tests**

Run: `go test ./internal/devcontainer/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/devcontainer/hooks.go internal/devcontainer/hooks_test.go internal/devcontainer/config.go internal/devcontainer/config_test.go internal/devcontainer/testdata/lifecycle.json
git commit -m "feat(devcontainer): parse lifecycle commands (string/array/object)"
```

### Task 1.8: Warn on unsupported fields

**Files:**
- Modify: `internal/devcontainer/config.go`
- Modify: `internal/devcontainer/config_test.go`
- Create: `internal/devcontainer/testdata/unsupported.json`

- [ ] **Step 1: Add fixture**

`internal/devcontainer/testdata/unsupported.json`:

```json
{
  "image": "ubuntu:24.04",
  "features": { "ghcr.io/devcontainers/features/node:1": {} },
  "runArgs": ["--cap-add=NET_ADMIN"],
  "forwardPorts": [3000],
  "customizations": { "vscode": { "extensions": [] } }
}
```

- [ ] **Step 2: Write failing test**

Append to `config_test.go`:

```go
func TestParse_UnsupportedFieldsCollected(t *testing.T) {
    dir := setupWorkspace(t, "unsupported.json")
    var got []string
    parseLogger = func(fields []string) { got = fields }
    t.Cleanup(func() { parseLogger = nil })
    _, err := Detect(dir)
    if err != nil {
        t.Fatalf("Detect: %v", err)
    }
    want := map[string]bool{"features": true, "runArgs": true, "forwardPorts": true, "customizations": true}
    for _, f := range got {
        delete(want, f)
    }
    if len(want) != 0 {
        t.Errorf("unsupported fields not collected: %v (got=%v)", want, got)
    }
}
```

- [ ] **Step 3: Run test to verify failure**

Run: `go test ./internal/devcontainer/ -run TestParse_UnsupportedFieldsCollected -v`
Expected: FAIL.

- [ ] **Step 4: Implement collection + warn hook**

Add to `config.go`:

```go
// parseLogger is a test hook for capturing the unsupported-fields warn payload.
// Production parse() calls ui.Warn directly via emitUnsupportedWarning.
var parseLogger func([]string)

var supportedFields = map[string]bool{
    "name":                 true,
    "image":                true,
    "build":                true,
    "remoteUser":           true,
    "containerUser":        true,
    "workspaceFolder":      true,
    "containerEnv":         true,
    "remoteEnv":            true,
    "mounts":               true,
    "initializeCommand":    true,
    "onCreateCommand":      true,
    "postCreateCommand":    true,
    "postStartCommand":     true,
    "updateRemoteUserUID":  true,
}

func collectUnsupported(top map[string]any) []string {
    var keys []string
    for k := range top {
        if !supportedFields[k] {
            keys = append(keys, k)
        }
    }
    sort.Strings(keys)
    return keys
}
```

In `parse()`, after the JSON unmarshal:

```go
if unsupported := collectUnsupported(top); len(unsupported) > 0 {
    if parseLogger != nil {
        parseLogger(unsupported)
    } else {
        emitUnsupportedWarning(path, unsupported)
    }
}
```

Add stub:

```go
// emitUnsupportedWarning prints a single ui.Warn listing fields that moat
// does not honor. Tests bypass this via parseLogger.
func emitUnsupportedWarning(path string, fields []string) {
    ui.Warnf("%s: ignoring unsupported devcontainer fields: %s",
        path, strings.Join(fields, ", "))
}
```

Add imports: `"sort"`, `"github.com/majorcontext/moat/internal/ui"`.

- [ ] **Step 5: Run tests**

Run: `go test ./internal/devcontainer/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/devcontainer/config.go internal/devcontainer/config_test.go internal/devcontainer/testdata/unsupported.json
git commit -m "feat(devcontainer): warn on unsupported fields (features, runArgs, etc.)"
```

### Task 1.9: Content hash for caching

**Files:**
- Modify: `internal/devcontainer/build.go` (new)
- Create: `internal/devcontainer/build_test.go`
- Create: `internal/devcontainer/testdata/hash-fixture/.devcontainer/devcontainer.json`
- Create: `internal/devcontainer/testdata/hash-fixture/.devcontainer/Dockerfile`

- [ ] **Step 1: Add fixture directory**

`internal/devcontainer/testdata/hash-fixture/.devcontainer/devcontainer.json`:

```json
{
  "build": { "dockerfile": "Dockerfile" }
}
```

`internal/devcontainer/testdata/hash-fixture/.devcontainer/Dockerfile`:

```dockerfile
FROM ubuntu:24.04
RUN echo hello
```

- [ ] **Step 2: Write failing test**

Create `internal/devcontainer/build_test.go`:

```go
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
```

- [ ] **Step 3: Run test to verify failure**

Run: `go test ./internal/devcontainer/ -run TestContentHash -v`
Expected: FAIL.

- [ ] **Step 4: Implement ContentHash**

Create `internal/devcontainer/build.go`:

```go
package devcontainer

import (
    "crypto/sha256"
    "encoding/hex"
    "fmt"
    "io"
    "os"
    "path/filepath"
    "sort"
)

// ContentHash returns a stable hex SHA-256 over every file under
// <workspace>/.devcontainer/. The hash depends only on relative paths and
// file contents, so identical configs at different workspace paths share
// the same hash (and thus the same cached image tag).
func ContentHash(workspace string) (string, error) {
    dcDir := filepath.Join(workspace, ".devcontainer")
    h := sha256.New()
    h.Write([]byte("DevcontainerBase"))
    var files []string
    if err := filepath.Walk(dcDir, func(p string, info os.FileInfo, err error) error {
        if err != nil {
            return err
        }
        if !info.Mode().IsRegular() {
            return nil
        }
        files = append(files, p)
        return nil
    }); err != nil {
        return "", fmt.Errorf("walk %s: %w", dcDir, err)
    }
    sort.Strings(files)
    for _, p := range files {
        rel, _ := filepath.Rel(dcDir, p)
        h.Write([]byte(rel))
        h.Write([]byte{0})
        f, err := os.Open(p)
        if err != nil {
            return "", err
        }
        if _, err := io.Copy(h, f); err != nil {
            f.Close()
            return "", err
        }
        f.Close()
        h.Write([]byte{0})
    }
    return hex.EncodeToString(h.Sum(nil)), nil
}
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/devcontainer/ -run TestContentHash -v`
Expected: PASS both subtests.

- [ ] **Step 6: Commit**

```bash
git add internal/devcontainer/build.go internal/devcontainer/build_test.go internal/devcontainer/testdata/hash-fixture/
git commit -m "feat(devcontainer): content-addressed hash for image caching"
```

### Task 1.10: BuildBase — image: case (write FROM Dockerfile and invoke BuildManager)

**Files:**
- Modify: `internal/devcontainer/build.go`
- Modify: `internal/devcontainer/build_test.go`

- [ ] **Step 1: Write failing test using fake BuildManager**

Append to `build_test.go`:

```go
import (
    "context"
    "strings"
    "github.com/majorcontext/moat/internal/container"
)

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
```

- [ ] **Step 2: Run test to verify failure**

Run: `go test ./internal/devcontainer/ -run TestBuildBase -v`
Expected: FAIL.

- [ ] **Step 3: Implement BuildBase (image: case only)**

Append to `build.go`:

```go
import (
    "context"
    "github.com/majorcontext/moat/internal/container"
)

// BuildOptions configures BuildBase.
type BuildOptions struct {
    NoCache bool // force rebuild
}

// BuildBase resolves the devcontainer's base image (Stage A). It returns
// a deterministic, content-addressed tag like
// "moat-devcontainer-<basename>:base-<sha[:12]>".
//
// If the tag already exists locally and NoCache is false, BuildBase is a
// no-op. Otherwise it builds the image via the runtime's BuildManager.
// The image: case writes a one-line "FROM <image>" Dockerfile so the same
// BuildManager interface handles both pulls and Dockerfile builds.
func BuildBase(ctx context.Context, bm container.BuildManager, workspace string, cfg *Config, opts BuildOptions) (string, error) {
    if cfg == nil {
        return "", fmt.Errorf("devcontainer config is nil")
    }
    hash, err := ContentHash(workspace)
    if err != nil {
        return "", err
    }
    tag := fmt.Sprintf("moat-devcontainer-%s:base-%s", filepath.Base(workspace), hash[:12])

    if !opts.NoCache {
        exists, err := bm.ImageExists(ctx, tag)
        if err != nil {
            return "", fmt.Errorf("checking %s: %w", tag, err)
        }
        if exists {
            return tag, nil
        }
    }

    if cfg.Build == nil {
        if cfg.Image == "" {
            return "", fmt.Errorf("devcontainer has no image or build.dockerfile")
        }
        df := fmt.Sprintf("FROM %s\n", cfg.Image)
        if err := bm.BuildImage(ctx, df, tag, container.BuildOptions{NoCache: opts.NoCache}); err != nil {
            return "", fmt.Errorf("staging %s: %w", cfg.Image, err)
        }
        return tag, nil
    }

    // Dockerfile build implemented in Task 1.11.
    return "", fmt.Errorf("build.dockerfile not yet implemented")
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/devcontainer/ -run TestBuildBase -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/devcontainer/build.go internal/devcontainer/build_test.go
git commit -m "feat(devcontainer): BuildBase for image: form via BuildManager"
```

### Task 1.11: BuildBase — build.dockerfile case

**Files:**
- Modify: `internal/devcontainer/build.go`
- Modify: `internal/devcontainer/build_test.go`

- [ ] **Step 1: Write failing test**

Append to `build_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify failure**

Run: `go test ./internal/devcontainer/ -run TestBuildBase_Dockerfile -v`
Expected: FAIL with "build.dockerfile not yet implemented".

- [ ] **Step 3: Implement the build.dockerfile path**

In `build.go`, replace the "not yet implemented" return with:

```go
    dcDir := filepath.Join(workspace, ".devcontainer")
    dfPath := filepath.Join(dcDir, cfg.Build.Dockerfile)
    dfBytes, err := os.ReadFile(dfPath)
    if err != nil {
        return "", fmt.Errorf("read %s: %w", dfPath, err)
    }
    bopts := container.BuildOptions{
        NoCache:      opts.NoCache,
        Target:       cfg.Build.Target,
        BuildArgs:    cfg.Build.Args,
        // Context is rooted at <.devcontainer>/<build.context>. Callers stitch
        // the build context into ContextFiles when needed.
        ContextFiles: map[string][]byte{},
    }
    // Always include the Dockerfile in the context so BuildImage can locate
    // it without depending on the host filesystem layout.
    bopts.ContextFiles["Dockerfile"] = dfBytes
    if err := bm.BuildImage(ctx, string(dfBytes), tag, bopts); err != nil {
        return "", fmt.Errorf("building devcontainer Dockerfile: %w", err)
    }
    return tag, nil
```

**Note for implementer:** verify `container.BuildOptions` has `Target` and `BuildArgs` fields. If not, add them in the same change (search for the struct in `internal/container/runtime.go`, around line 389) and update `dockerBuildManager` and `appleBuildManager` to honor them. Add a brief unit test confirming `--target` and `--build-arg` make it onto the underlying invocation. If adding fields, also update any other callers in `internal/run/manager.go` so the build doesn't break.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/devcontainer/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/devcontainer/build.go internal/devcontainer/build_test.go internal/container/
git commit -m "feat(devcontainer): BuildBase for build.dockerfile form"
```

### Task 1.12: containerEnv overlay

**Files:**
- Modify: `internal/devcontainer/build.go`
- Modify: `internal/devcontainer/build_test.go`

- [ ] **Step 1: Write failing test**

Append to `build_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify failure**

Run: `go test ./internal/devcontainer/ -run TestBuildBase_ContainerEnv -v`
Expected: FAIL.

- [ ] **Step 3: Implement EnvOverlay and chain it after the base build**

Add to `build.go`:

```go
// envOverlayDockerfile bakes containerEnv keys into the image as ENV lines.
func envOverlayDockerfile(baseTag string, env map[string]string) string {
    if len(env) == 0 {
        return ""
    }
    keys := make([]string, 0, len(env))
    for k := range env {
        keys = append(keys, k)
    }
    sort.Strings(keys)
    var b strings.Builder
    fmt.Fprintf(&b, "FROM %s\n", baseTag)
    for _, k := range keys {
        fmt.Fprintf(&b, "ENV %s=%q\n", k, env[k])
    }
    return b.String()
}
```

Add `"strings"` import.

At the end of `BuildBase`, before each `return tag, nil`, fold in the overlay:

```go
if len(cfg.ContainerEnv) > 0 {
    overlay := envOverlayDockerfile(tag, cfg.ContainerEnv)
    if err := bm.BuildImage(ctx, overlay, tag, container.BuildOptions{NoCache: opts.NoCache}); err != nil {
        return "", fmt.Errorf("baking containerEnv: %w", err)
    }
}
return tag, nil
```

(Apply at both the `image:` return and the `build.dockerfile` return.)

- [ ] **Step 4: Run tests**

Run: `go test ./internal/devcontainer/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/devcontainer/build.go internal/devcontainer/build_test.go
git commit -m "feat(devcontainer): bake containerEnv as ENV overlay"
```

### Task 1.13: initializeCommand (host execution)

**Files:**
- Modify: `internal/devcontainer/hooks.go`
- Modify: `internal/devcontainer/hooks_test.go`

- [ ] **Step 1: Write failing test**

Append to `hooks_test.go`:

```go
import (
    "context"
    "os"
    "path/filepath"
    "testing"
)

func TestRunInitializeCommand_SuccessAndCwd(t *testing.T) {
    dir := t.TempDir()
    marker := filepath.Join(dir, "marker")
    cmd := fmt.Sprintf("pwd > %q", marker)
    if err := RunInitializeCommand(context.Background(), cmd, dir); err != nil {
        t.Fatalf("RunInitializeCommand: %v", err)
    }
    data, err := os.ReadFile(marker)
    if err != nil {
        t.Fatalf("marker: %v", err)
    }
    got := strings.TrimSpace(string(data))
    if got != dir {
        t.Errorf("pwd = %q, want %q", got, dir)
    }
}

func TestRunInitializeCommand_NonZeroExitIsHardFail(t *testing.T) {
    err := RunInitializeCommand(context.Background(), "false", t.TempDir())
    if err == nil {
        t.Fatal("expected error from `false` command")
    }
}

func TestRunInitializeCommand_EmptyIsNoop(t *testing.T) {
    if err := RunInitializeCommand(context.Background(), "", t.TempDir()); err != nil {
        t.Errorf("empty command: got err %v, want nil", err)
    }
}
```

Add the `"fmt"` import to `hooks_test.go`.

- [ ] **Step 2: Run test to verify failure**

Run: `go test ./internal/devcontainer/ -run TestRunInitializeCommand -v`
Expected: FAIL.

- [ ] **Step 3: Implement RunInitializeCommand**

Append to `hooks.go`:

```go
import (
    "context"
    "fmt"
    "os"
    "os/exec"
)

// RunInitializeCommand runs the devcontainer initializeCommand on the host
// with the workspace as cwd, inheriting the host's environment. A non-zero
// exit code is a hard failure. An empty command is a no-op.
func RunInitializeCommand(ctx context.Context, command, workspace string) error {
    if command == "" {
        return nil
    }
    cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
    cmd.Dir = workspace
    cmd.Stdout = os.Stderr
    cmd.Stderr = os.Stderr
    if err := cmd.Run(); err != nil {
        return fmt.Errorf("initializeCommand failed: %w", err)
    }
    return nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/devcontainer/ -run TestRunInitializeCommand -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/devcontainer/hooks.go internal/devcontainer/hooks_test.go
git commit -m "feat(devcontainer): run initializeCommand on host"
```

### Task 1.14: In-container lifecycle hooks (RunHook)

**Files:**
- Modify: `internal/devcontainer/hooks.go`
- Modify: `internal/devcontainer/hooks_test.go`

- [ ] **Step 1: Write failing test**

Append to `hooks_test.go`:

```go
import (
    "bytes"
    "io"
)

type fakeExecRuntime struct {
    calls []fakeExec
    fail  bool
}

type fakeExec struct {
    id       string
    cmd      []string
    stdinLen int
}

func (f *fakeExecRuntime) Exec(ctx context.Context, id string, cmd []string, stdin []byte, stdout, stderr io.Writer) error {
    f.calls = append(f.calls, fakeExec{id, cmd, len(stdin)})
    if f.fail {
        return &container.ExecError{ExitCode: 7}
    }
    fmt.Fprintln(stdout, "ok")
    return nil
}

func TestRunHook_PassesUserHomeAndCwd(t *testing.T) {
    fr := &fakeExecRuntime{}
    out := &bytes.Buffer{}
    err := RunHook(context.Background(), fr, "ctr-1", "onCreate", "echo hi",
        HookOpts{
            User:    "vscode",
            Home:    "/home/vscode",
            Workdir: "/workspaces/repo",
            Env:     map[string]string{"PATH": "/usr/local/bin:/usr/bin"},
        }, out, out)
    if err != nil {
        t.Fatalf("RunHook: %v", err)
    }
    if len(fr.calls) != 1 {
        t.Fatalf("got %d calls, want 1", len(fr.calls))
    }
    cmd := fr.calls[0].cmd
    joined := strings.Join(cmd, " ")
    for _, want := range []string{"sh", "-c", "cd /workspaces/repo && echo hi"} {
        if !strings.Contains(joined, want) {
            t.Errorf("cmd missing %q: %v", want, cmd)
        }
    }
}

func TestRunHook_NonZeroIsErrorForRequiredHook(t *testing.T) {
    fr := &fakeExecRuntime{fail: true}
    err := RunHook(context.Background(), fr, "ctr-1", "onCreate", "false", HookOpts{}, io.Discard, io.Discard)
    if err == nil {
        t.Fatal("expected error for failing required hook")
    }
}

func TestRunHook_EmptyCommandIsNoop(t *testing.T) {
    fr := &fakeExecRuntime{}
    if err := RunHook(context.Background(), fr, "ctr-1", "onCreate", "", HookOpts{}, io.Discard, io.Discard); err != nil {
        t.Errorf("empty: got %v", err)
    }
    if len(fr.calls) != 0 {
        t.Errorf("empty hook should not call Exec")
    }
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `go test ./internal/devcontainer/ -run TestRunHook -v`
Expected: FAIL.

- [ ] **Step 3: Implement RunHook**

Append to `hooks.go`:

```go
// HookOpts configures the user, working dir, and env for a hook exec.
type HookOpts struct {
    User    string
    Home    string
    Workdir string
    Env     map[string]string
}

// ExecRuntime is the minimal subset of container.Runtime we need. Lets us
// stub the runtime in tests without depending on the full interface.
type ExecRuntime interface {
    Exec(ctx context.Context, id string, cmd []string, stdin []byte, stdout, stderr io.Writer) error
}

// RunHook runs a single in-container devcontainer lifecycle hook
// (onCreate/postCreate/postStart) via the runtime's Exec. A non-zero exit
// returns an error; the caller decides hard-fail vs. warn-and-continue.
func RunHook(ctx context.Context, rt ExecRuntime, containerID, name, command string, opts HookOpts, stdout, stderr io.Writer) error {
    if command == "" {
        return nil
    }
    // We can't pass workdir or env via Exec directly, so wrap the command:
    // cd <workdir> && <command>, prepending env exports.
    var b strings.Builder
    if opts.Workdir != "" {
        fmt.Fprintf(&b, "cd %s && ", shellQuote(opts.Workdir))
    }
    keys := make([]string, 0, len(opts.Env))
    for k := range opts.Env {
        keys = append(keys, k)
    }
    sort.Strings(keys)
    for _, k := range keys {
        fmt.Fprintf(&b, "export %s=%s; ", k, shellQuote(opts.Env[k]))
    }
    if opts.Home != "" {
        fmt.Fprintf(&b, "export HOME=%s; ", shellQuote(opts.Home))
    }
    if opts.User != "" {
        fmt.Fprintf(&b, "export USER=%s; ", shellQuote(opts.User))
    }
    b.WriteString(command)
    shellCmd := []string{"/bin/sh", "-lc", b.String()}
    if err := rt.Exec(ctx, containerID, shellCmd, nil, stdout, stderr); err != nil {
        return fmt.Errorf("%s: %w", name, err)
    }
    return nil
}

func shellQuote(s string) string {
    return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
```

Add `"io"` and `"sort"` imports (sort is already used by the warn collection in config.go but each file needs its own).

- [ ] **Step 4: Run tests**

Run: `go test ./internal/devcontainer/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/devcontainer/hooks.go internal/devcontainer/hooks_test.go
git commit -m "feat(devcontainer): in-container lifecycle hook execution"
```

### Task 1.15: ProbeUserEnv (login-shell env extraction)

**Files:**
- Create: `internal/devcontainer/probe.go`
- Create: `internal/devcontainer/probe_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/devcontainer/probe_test.go`:

```go
package devcontainer

import (
    "bytes"
    "context"
    "io"
    "strings"
    "testing"
)

type fakeProbeRuntime struct {
    stdouts []string // returned for each Exec call in order
    calls   int
}

func (f *fakeProbeRuntime) Exec(ctx context.Context, id string, cmd []string, stdin []byte, stdout, stderr io.Writer) error {
    idx := f.calls
    f.calls++
    if idx < len(f.stdouts) {
        io.WriteString(stdout, f.stdouts[idx])
    }
    return nil
}

func TestProbeUserEnv_ParsesProcEnviron(t *testing.T) {
    marker := "MARK123"
    body := "PATH=/usr/local/bin:/usr/bin\x00FOO=bar\x00PWD=/should/drop\x00"
    fr := &fakeProbeRuntime{stdouts: []string{marker + body + marker}}
    // Override the mark generator for determinism.
    env, err := probeUserEnvWithMark(context.Background(), fr, "ctr", "vscode", marker)
    if err != nil {
        t.Fatalf("probe: %v", err)
    }
    if env["PATH"] != "/usr/local/bin:/usr/bin" {
        t.Errorf("PATH = %q", env["PATH"])
    }
    if env["FOO"] != "bar" {
        t.Errorf("FOO = %q", env["FOO"])
    }
    if _, ok := env["PWD"]; ok {
        t.Errorf("PWD should be dropped")
    }
}

func TestProbeUserEnv_DedupsPath(t *testing.T) {
    marker := "M"
    body := "PATH=/a:/b:/a:/c\x00"
    fr := &fakeProbeRuntime{stdouts: []string{marker + body + marker}}
    env, _ := probeUserEnvWithMark(context.Background(), fr, "ctr", "root", marker)
    if env["PATH"] != "/a:/b:/c" {
        t.Errorf("PATH = %q, want /a:/b:/c", env["PATH"])
    }
}

func TestProbeUserEnv_FallsBackToPrintenv(t *testing.T) {
    marker := "M"
    // First call returns no markers (simulating /proc failure).
    // Second call returns valid printenv output with markers.
    fr := &fakeProbeRuntime{stdouts: []string{
        "",
        marker + "PATH=/bin\n" + marker,
    }}
    env, err := probeUserEnvWithMark(context.Background(), fr, "ctr", "root", marker)
    if err != nil {
        t.Fatalf("probe: %v", err)
    }
    if env["PATH"] != "/bin" {
        t.Errorf("PATH = %q, want /bin", env["PATH"])
    }
}

var _ = bytes.NewBuffer // keep import
var _ = strings.Contains
```

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/devcontainer/ -run TestProbeUserEnv -v`
Expected: FAIL.

- [ ] **Step 3: Implement probe.go**

Create `internal/devcontainer/probe.go`:

```go
package devcontainer

import (
    "bytes"
    "context"
    "crypto/rand"
    "encoding/hex"
    "fmt"
    "io"
    "strings"
)

// ProbeUserEnv runs the user's login shell inside the container and
// returns the resulting environment. This is needed because lifecycle
// hooks must run with PATH, locale, etc. set by /etc/profile, conda
// init, nvm, etc. — exec-style invocations don't get that for free.
//
// Probe strategy: print a UUID marker, dump /proc/self/environ
// (null-separated) between markers, print the marker again. Fall back
// to `printenv` (newline-separated) when /proc fails.
func ProbeUserEnv(ctx context.Context, rt ExecRuntime, containerID, user string) (map[string]string, error) {
    return probeUserEnvWithMark(ctx, rt, containerID, user, newMark())
}

func probeUserEnvWithMark(ctx context.Context, rt ExecRuntime, containerID, user, mark string) (map[string]string, error) {
    env, err := probeWith(ctx, rt, containerID, user, mark, "cat /proc/self/environ", "\x00")
    if err == nil && env != nil {
        return finishEnv(env), nil
    }
    env, err = probeWith(ctx, rt, containerID, user, mark, "printenv", "\n")
    if err != nil {
        return nil, err
    }
    if env == nil {
        return map[string]string{}, nil
    }
    return finishEnv(env), nil
}

func probeWith(ctx context.Context, rt ExecRuntime, containerID, user, mark, cmd, sep string) (map[string]string, error) {
    inner := fmt.Sprintf("echo -n %s; %s; echo -n %s", mark, cmd, mark)
    args := []string{"/bin/sh", "-lc", inner}
    var out bytes.Buffer
    if err := rt.Exec(ctx, containerID, args, nil, &out, io.Discard); err != nil {
        return nil, nil // try fallback
    }
    raw := out.String()
    start := strings.Index(raw, mark)
    end := strings.LastIndex(raw, mark)
    if start == -1 || end == -1 || end == start {
        return nil, nil
    }
    body := raw[start+len(mark) : end]
    if body == "" {
        return nil, nil
    }
    env := map[string]string{}
    for _, entry := range strings.Split(body, sep) {
        if i := strings.Index(entry, "="); i != -1 {
            env[entry[:i]] = entry[i+1:]
        }
    }
    return env, nil
}

func finishEnv(env map[string]string) map[string]string {
    delete(env, "PWD")
    delete(env, "SHLVL")
    delete(env, "_")
    if p, ok := env["PATH"]; ok {
        env["PATH"] = dedupPath(p)
    }
    return env
}

func dedupPath(p string) string {
    seen := map[string]bool{}
    out := make([]string, 0)
    for _, part := range strings.Split(p, ":") {
        if !seen[part] {
            seen[part] = true
            out = append(out, part)
        }
    }
    return strings.Join(out, ":")
}

func newMark() string {
    var b [16]byte
    _, _ = rand.Read(b[:])
    return hex.EncodeToString(b[:])
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/devcontainer/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/devcontainer/probe.go internal/devcontainer/probe_test.go
git commit -m "feat(devcontainer): probe login-shell env via /proc/self/environ"
```

### Task 1.16: PR 1 wrap-up

- [ ] **Step 1: Run the whole package test suite**

Run: `go test ./internal/devcontainer/ -race -v`
Expected: PASS.

- [ ] **Step 2: Lint**

Run: `make lint` (or `go vet ./internal/devcontainer/`).
Expected: clean.

- [ ] **Step 3: Open PR 1**

```bash
gh pr create
```

Title: `feat(devcontainer): parser, builder, hooks, env probe`
Body summary: lists the files and notes "dead code; integration in follow-up PR."

---

## PR 2 — Manager integration

This PR wires the devcontainer package into `internal/run/manager.go`. After this PR ships, `moat run` in a devcontainer-equipped workspace works end-to-end.

### Task 2.1: Add UID-remap fields to ImageSpec

**Files:**
- Modify: `internal/deps/imagespec.go`
- Modify: `internal/deps/imagespec_test.go` (create if absent)

- [ ] **Step 1: Write failing tests**

In `internal/deps/imagespec_test.go`:

```go
package deps

import "testing"

func TestNeedsCustomImage_RemapUser(t *testing.T) {
    s := &ImageSpec{RemapUser: "vscode", RemapUID: 1000, RemapGID: 1000}
    if !s.NeedsCustomImage(false) {
        t.Error("RemapUser should trigger NeedsCustomImage")
    }
}
```

- [ ] **Step 2: Run test to confirm failure**

Run: `go test ./internal/deps/ -run TestNeedsCustomImage_RemapUser -v`
Expected: FAIL (field undefined).

- [ ] **Step 3: Add fields**

In `internal/deps/imagespec.go`, inside the `ImageSpec` struct, add:

```go
    // RemapUser is the in-container username whose UID/GID should be remapped
    // to RemapUID/RemapGID at image build time. Empty means no remap.
    // Used by devcontainer mode on Linux so that files inside the workspace
    // mount remain owned by the host workspace owner.
    RemapUser string
    RemapUID  int
    RemapGID  int
```

In `NeedsCustomImage`, append `|| s.RemapUser != ""` to the return expression.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/deps/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/deps/imagespec.go internal/deps/imagespec_test.go
git commit -m "feat(deps): add RemapUser/RemapUID/RemapGID to ImageSpec"
```

### Task 2.2: Include RemapUser in image hash

**Files:**
- Modify: `internal/deps/resolve.go` (where `ImageTag` is defined; find via grep)
- Modify: `internal/deps/resolve_test.go`

- [ ] **Step 1: Locate the hash function**

Run: `grep -n "func ImageTag" /workspace/internal/deps/*.go`
Open the file containing `ImageTag` and the helper that aggregates hash components from `ImageSpec`.

- [ ] **Step 2: Write a failing test**

In `internal/deps/resolve_test.go` add:

```go
func TestImageTag_VariesByRemapUID(t *testing.T) {
    a := ImageTag(nil, &ImageSpec{BaseImage: "ubuntu:24.04", RemapUser: "vscode", RemapUID: 1000, RemapGID: 1000})
    b := ImageTag(nil, &ImageSpec{BaseImage: "ubuntu:24.04", RemapUser: "vscode", RemapUID: 1001, RemapGID: 1001})
    if a == b {
        t.Errorf("tag should differ when RemapUID differs: %s == %s", a, b)
    }
}
```

- [ ] **Step 3: Run test to verify failure**

Run: `go test ./internal/deps/ -run TestImageTag_VariesByRemapUID -v`
Expected: FAIL.

- [ ] **Step 4: Add hash components**

In the helper that builds the hash input (a `[]string` of stable components), append:

```go
if s.RemapUser != "" {
    components = append(components, fmt.Sprintf("remap:%s:%d:%d", s.RemapUser, s.RemapUID, s.RemapGID))
}
```

(Use the exact variable name in the existing function; this snippet is a template.)

- [ ] **Step 5: Run tests**

Run: `go test ./internal/deps/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/deps/resolve.go internal/deps/resolve_test.go
git commit -m "feat(deps): include RemapUser in image tag hash"
```

### Task 2.3: Emit UID-remap RUN block in generated Dockerfile

**Files:**
- Modify: `internal/deps/dockerfile.go`
- Modify: `internal/deps/dockerfile_test.go`

- [ ] **Step 1: Write a failing test**

Append to `internal/deps/dockerfile_test.go`:

```go
func TestGenerateDockerfile_AppendsUIDRemap(t *testing.T) {
    spec := &ImageSpec{
        BaseImage: "ubuntu:24.04",
        RemapUser: "vscode",
        RemapUID:  1234,
        RemapGID:  1234,
    }
    out, err := GenerateDockerfile(nil, spec)
    if err != nil {
        t.Fatalf("GenerateDockerfile: %v", err)
    }
    df := out.Dockerfile
    for _, want := range []string{
        "ARG MOAT_USER=vscode",
        "ARG MOAT_UID=1234",
        "ARG MOAT_GID=1234",
        "groupmod -o -g",
        "usermod  -o -u",
    } {
        if !strings.Contains(df, want) {
            t.Errorf("dockerfile missing %q\n--- dockerfile ---\n%s", want, df)
        }
    }
}

func TestGenerateDockerfile_NoUIDRemapForRoot(t *testing.T) {
    spec := &ImageSpec{BaseImage: "ubuntu:24.04"}
    out, _ := GenerateDockerfile(nil, spec)
    if strings.Contains(out.Dockerfile, "MOAT_UID") {
        t.Errorf("should not emit MOAT_UID for spec without RemapUser")
    }
}
```

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/deps/ -run TestGenerateDockerfile_AppendsUIDRemap -v`
Expected: FAIL.

- [ ] **Step 3: Add the emit block**

In `internal/deps/dockerfile.go`, just before `writeEntrypoint(&b, ...)`:

```go
writeUIDRemap(&b, opts)
```

Add helper:

```go
// writeUIDRemap appends a RUN block that remaps a non-root user's UID/GID
// to host values at image build time. No-op when RemapUser is empty or
// equals "root". The chown step is best-effort.
func writeUIDRemap(b *strings.Builder, opts *ImageSpec) {
    if opts == nil || opts.RemapUser == "" || opts.RemapUser == "root" {
        return
    }
    fmt.Fprintf(b, "ARG MOAT_USER=%s\n", opts.RemapUser)
    fmt.Fprintf(b, "ARG MOAT_UID=%d\n", opts.RemapUID)
    fmt.Fprintf(b, "ARG MOAT_GID=%d\n", opts.RemapGID)
    b.WriteString(`RUN if [ "$MOAT_USER" != "root" ] && id "$MOAT_USER" >/dev/null 2>&1; then \
      groupmod -o -g "$MOAT_GID" "$(id -gn "$MOAT_USER")" && \
      usermod  -o -u "$MOAT_UID" "$MOAT_USER" && \
      chown -R "$MOAT_UID:$MOAT_GID" "$(getent passwd "$MOAT_USER" | cut -d: -f6)" 2>/dev/null || true; \
    fi
`)
    b.WriteString("\n")
}
```

Make sure `"fmt"` is imported.

- [ ] **Step 4: Run all deps tests**

Run: `go test ./internal/deps/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/deps/dockerfile.go internal/deps/dockerfile_test.go
git commit -m "feat(deps): emit UID-remap RUN block when RemapUser is set"
```

### Task 2.4: Add DevcontainerHash and lifecycle hook fields to Run struct

**Files:**
- Modify: `internal/run/run.go` (or wherever `Run` struct is defined; check with `grep`)
- Modify: tests that construct `Run` literals

- [ ] **Step 1: Locate the Run struct**

Run: `grep -n "type Run struct" /workspace/internal/run/*.go`

- [ ] **Step 2: Add fields**

In the `Run` struct, add:

```go
    // DevcontainerHash is the sha256 of .devcontainer/ contents at run creation.
    // Empty if no devcontainer was used. Compared against the live workspace
    // at status time to surface drift hints.
    DevcontainerHash string `json:"devcontainerHash,omitempty"`

    // PostStartCmd is the devcontainer postStartCommand. Persisted so that
    // restarts re-run it. Empty when no devcontainer is used.
    PostStartCmd string `json:"postStartCmd,omitempty"`

    // PostStartUser/Home/Workdir record the exec context for PostStartCmd.
    PostStartUser    string `json:"postStartUser,omitempty"`
    PostStartHome    string `json:"postStartHome,omitempty"`
    PostStartWorkdir string `json:"postStartWorkdir,omitempty"`
```

- [ ] **Step 3: Verify all callsites still compile**

Run: `go build ./...`
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add internal/run/
git commit -m "feat(run): persist devcontainer hash and postStart hook on Run"
```

### Task 2.5: Detect devcontainer in Manager.Create and apply precedence

**Files:**
- Modify: `internal/run/manager.go`
- Modify: `internal/run/manager_test.go`

- [ ] **Step 1: Write failing test for precedence rule**

In `internal/run/manager_test.go`:

```go
func TestManager_DevcontainerPrecedence(t *testing.T) {
    cases := []struct {
        name            string
        configBaseImage string
        configDeps      []string
        hasDevcontainer bool
        wantUse         bool
    }{
        {"no-dc-no-base", "", nil, false, false},
        {"no-dc-with-base", "x:1", nil, false, false},
        {"dc-no-config", "", nil, true, true},
        {"dc-config-silent", "", nil, true, true},
        {"dc-config-with-base", "x:1", nil, true, false},
        {"dc-config-with-deps", "", []string{"node:20"}, true, false},
    }
    for _, c := range cases {
        t.Run(c.name, func(t *testing.T) {
            var cfg *config.Config
            if c.configBaseImage != "" || c.configDeps != nil {
                cfg = &config.Config{BaseImage: c.configBaseImage, Dependencies: c.configDeps}
            }
            var dc *devcontainer.Config
            if c.hasDevcontainer {
                dc = &devcontainer.Config{Image: "ubuntu:24.04"}
            }
            got := useDevcontainerForImage(cfg, dc)
            if got != c.wantUse {
                t.Errorf("%s: got %v, want %v", c.name, got, c.wantUse)
            }
        })
    }
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `go test ./internal/run/ -run TestManager_DevcontainerPrecedence -v`
Expected: FAIL.

- [ ] **Step 3: Add the helper**

In `internal/run/manager.go`, near the top of the file or alongside `resolveBaseImage`:

```go
// useDevcontainerForImage returns true when the devcontainer should drive
// the base image. moat.yaml's base_image: or dependencies: take precedence;
// otherwise the devcontainer wins.
func useDevcontainerForImage(cfg *config.Config, dc *devcontainer.Config) bool {
    if dc == nil {
        return false
    }
    if cfg == nil {
        return true
    }
    return cfg.BaseImage == "" && len(cfg.Dependencies) == 0
}
```

Add import: `"github.com/majorcontext/moat/internal/devcontainer"`.

- [ ] **Step 4: Detect at the top of Manager.Create**

In `Manager.Create`, after `opts.Workspace` is known and before the `imageSpec :=` construction (around line 1742):

```go
dcCfg, dcErr := devcontainer.Detect(opts.Workspace)
if dcErr != nil {
    return nil, fmt.Errorf("parse devcontainer.json: %w", dcErr)
}
if opts.NoDevcontainer {
    dcCfg = nil
}
useDC := useDevcontainerForImage(opts.Config, dcCfg)
if dcCfg != nil && !useDC && (opts.Config != nil && (opts.Config.BaseImage != "" || len(opts.Config.Dependencies) > 0)) {
    ui.Warnf("devcontainer.json detected but ignored: moat.yaml specifies base_image or dependencies")
}
```

(`opts.NoDevcontainer` is added in PR 3 Task 3.1. For now, define it on `RunOptions` as a `bool` with zero-value default so this code compiles.)

- [ ] **Step 5: Add `NoDevcontainer bool` to RunOptions**

Find the `RunOptions` struct (likely in `internal/run/manager.go` or `run.go`) and add:

```go
    // NoDevcontainer forces moat to ignore .devcontainer/devcontainer.json.
    NoDevcontainer bool
```

- [ ] **Step 6: Run tests**

Run: `go test ./internal/run/ -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/run/
git commit -m "feat(run): detect devcontainer and decide image precedence"
```

### Task 2.6: Build Stage A and feed tag into ImageSpec

**Files:**
- Modify: `internal/run/manager.go`

- [ ] **Step 1: Write integration-style test**

Create `internal/run/devcontainer_integration_test.go`:

```go
package run

import (
    "context"
    "os"
    "path/filepath"
    "strings"
    "testing"

    "github.com/majorcontext/moat/internal/container"
)

func TestManager_DevcontainerStageA_SetsBaseImage(t *testing.T) {
    workspace := t.TempDir()
    dcDir := filepath.Join(workspace, ".devcontainer")
    os.MkdirAll(dcDir, 0o755)
    os.WriteFile(filepath.Join(dcDir, "devcontainer.json"), []byte(`{"image":"ubuntu:24.04"}`), 0o644)

    m := newTestManager(t) // existing test helper; mirrors current tests
    spec, dcTag, err := m.resolveImageSpecForDevcontainer(context.Background(), CreateOptions{
        Workspace: workspace,
        Grants:    []string{"github"},
        Config:    nil,
    })
    if err != nil {
        t.Fatalf("resolve: %v", err)
    }
    if !strings.HasPrefix(dcTag, "moat-devcontainer-") {
        t.Errorf("dcTag = %q", dcTag)
    }
    if spec.BaseImage != dcTag {
        t.Errorf("spec.BaseImage = %q, want %q", spec.BaseImage, dcTag)
    }
}
```

This test assumes a refactored seam: a new function `resolveImageSpecForDevcontainer` on `Manager`. We introduce it as the integration boundary.

- [ ] **Step 2: Run test to verify failure**

Run: `go test ./internal/run/ -run TestManager_DevcontainerStageA -v`
Expected: FAIL.

- [ ] **Step 3: Wire devcontainer.BuildBase into Create**

In `Manager.Create`, after the precedence decision:

```go
var dcBaseTag string
if useDC {
    // Stage A: run initializeCommand, build base, bake containerEnv overlay.
    if err := devcontainer.RunInitializeCommand(ctx, dcCfg.InitializeCmd, opts.Workspace); err != nil {
        return nil, err
    }
    bm := m.defaultRuntime().BuildManager()
    if bm == nil {
        return nil, fmt.Errorf("runtime %s does not support image building (needed for devcontainer)", m.defaultRuntime().Type())
    }
    tag, err := devcontainer.BuildBase(ctx, bm, opts.Workspace, dcCfg, devcontainer.BuildOptions{NoCache: opts.Rebuild})
    if err != nil {
        return nil, fmt.Errorf("building devcontainer base: %w", err)
    }
    dcBaseTag = tag
}
```

Then, when constructing `imageSpec`, prefer the devcontainer base over the moat.yaml base:

```go
specBase := resolveBaseImage(opts.Config)
if dcBaseTag != "" {
    specBase = dcBaseTag
}
imageSpec := &deps.ImageSpec{
    BaseImage: specBase,
    ...
}
```

For Linux remap, add (only when `useDC` and `runtime.GOOS == "linux"` and `dcCfg.User != "root"` and `dcCfg.UpdateRemoteUserUID`):

```go
if useDC && runtime.GOOS == "linux" && dcCfg.User != "root" && dcCfg.UpdateRemoteUserUID {
    uid, gid := getWorkspaceOwner(opts.Workspace)
    imageSpec.RemapUser = dcCfg.User
    imageSpec.RemapUID = uid
    imageSpec.RemapGID = gid
}
```

Add `"runtime"` import.

Refactor far enough that the test in Step 1 has a clean entry point — e.g., extract the `imageSpec`-building block into a method `resolveImageSpec(ctx, opts) (*deps.ImageSpec, string, error)` that returns the spec and the devcontainer base tag (for the test to assert on).

- [ ] **Step 4: Run tests**

Run: `go test ./internal/run/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/run/
git commit -m "feat(run): build devcontainer Stage A and plumb into ImageSpec"
```

### Task 2.7: Override user, workspaceFolder, mounts, env from devcontainer

**Files:**
- Modify: `internal/run/manager.go`

- [ ] **Step 1: Write failing assertions**

Extend the integration test from Task 2.6 with:

```go
func TestManager_DevcontainerOverridesUserAndWorkdir(t *testing.T) {
    workspace := t.TempDir()
    dcDir := filepath.Join(workspace, ".devcontainer")
    os.MkdirAll(dcDir, 0o755)
    os.WriteFile(filepath.Join(dcDir, "devcontainer.json"), []byte(`{
      "image": "ubuntu:24.04",
      "remoteUser": "vscode",
      "workspaceFolder": "/work/repo",
      "containerEnv": { "FOO": "bar" },
      "remoteEnv":    { "BAZ": "qux" }
    }`), 0o644)

    m := newTestManager(t)
    plan, err := m.planContainerForTest(context.Background(), CreateOptions{Workspace: workspace})
    if err != nil {
        t.Fatal(err)
    }
    if plan.User != "vscode" {
        t.Errorf("User = %q, want vscode", plan.User)
    }
    if plan.WorkingDir != "/work/repo" {
        t.Errorf("WorkingDir = %q", plan.WorkingDir)
    }
    foundWorkspaceMount := false
    for _, mnt := range plan.Mounts {
        if mnt.Target == "/work/repo" && mnt.Source == workspace {
            foundWorkspaceMount = true
        }
    }
    if !foundWorkspaceMount {
        t.Errorf("workspace not mounted at /work/repo: %+v", plan.Mounts)
    }
    hasRemoteEnv := false
    for _, e := range plan.Env {
        if e == "BAZ=qux" {
            hasRemoteEnv = true
        }
    }
    if !hasRemoteEnv {
        t.Errorf("BAZ=qux missing from container env: %v", plan.Env)
    }
}
```

`planContainerForTest` is a new test seam that returns the assembled `container.Config` before container creation.

- [ ] **Step 2: Run test to verify failure**

Run: `go test ./internal/run/ -run TestManager_DevcontainerOverridesUserAndWorkdir -v`
Expected: FAIL.

- [ ] **Step 3: Modify workspace-mount logic**

In `Manager.Create`, when `useDC`:

- Replace the `/workspace` target with `dcCfg.WorkspaceFolder` (default to `/workspaces/<basename>`).
- Set the container `User` to `dcCfg.User`.
- Set `WorkingDir` to the resolved workspace folder.
- Append `dcCfg.Mounts` to the mounts list, translating to `container.MountConfig`. If a target already in the mount list collides, log a warning and keep the devcontainer entry.
- Append `dcCfg.RemoteEnv` to the container env list (after the existing moat injection and before moat.yaml `env:`; see Section 6 of the design).
- Persist `dcCfg.WorkspaceFolder`, `dcCfg.User`, `dcCfg.Home` to the `Run` struct so `moat exec` defaults match.

Snippet (place inline next to the existing workspace-mount loop):

```go
workspaceTarget := "/workspace"
if useDC {
    workspaceTarget = dcCfg.WorkspaceFolder
    if workspaceTarget == "" {
        workspaceTarget = "/workspaces/" + filepath.Base(opts.Workspace)
    }
}
// ... existing hasExplicitWorkspace logic, using workspaceTarget ...
mounts = append(mounts, container.MountConfig{
    Source: opts.Workspace, Target: workspaceTarget, ReadOnly: false,
})

if useDC {
    for _, m := range dcCfg.Mounts {
        mounts = append(mounts, container.MountConfig{
            Source: m.Source, Target: m.Target, ReadOnly: m.ReadOnly,
        })
    }
}
```

For env:

```go
if useDC {
    for k, v := range dcCfg.RemoteEnv {
        envList = append(envList, fmt.Sprintf("%s=%s", k, v))
    }
}
```

For container user:

```go
containerCfg.User = dcCfg.User
containerCfg.WorkingDir = workspaceTarget
```

Store on `Run`:

```go
r.PostStartCmd = dcCfg.PostStartCmd
r.PostStartUser = dcCfg.User
r.PostStartHome = dcCfg.Home
r.PostStartWorkdir = workspaceTarget
hash, _ := devcontainer.ContentHash(opts.Workspace)
r.DevcontainerHash = hash
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/run/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/run/
git commit -m "feat(run): honor devcontainer user, workspaceFolder, mounts, remoteEnv"
```

### Task 2.8: Run onCreate, postCreate, postStart inside the container

**Files:**
- Modify: `internal/run/manager.go`

- [ ] **Step 1: Write integration test**

Append to `devcontainer_integration_test.go`:

```go
func TestManager_DevcontainerLifecycleHooks(t *testing.T) {
    workspace := t.TempDir()
    dcDir := filepath.Join(workspace, ".devcontainer")
    os.MkdirAll(dcDir, 0o755)
    os.WriteFile(filepath.Join(dcDir, "devcontainer.json"), []byte(`{
      "image": "ubuntu:24.04",
      "onCreateCommand": "echo onCreate",
      "postCreateCommand": "echo postCreate",
      "postStartCommand": "echo postStart"
    }`), 0o644)

    m, fakeRT := newTestManagerWithFakeExec(t)
    _, err := m.Create(context.Background(), CreateOptions{Workspace: workspace})
    if err != nil {
        t.Fatalf("Create: %v", err)
    }
    var seen []string
    for _, c := range fakeRT.Calls {
        if strings.Contains(c.Cmd, "echo onCreate") {
            seen = append(seen, "onCreate")
        }
        if strings.Contains(c.Cmd, "echo postCreate") {
            seen = append(seen, "postCreate")
        }
        if strings.Contains(c.Cmd, "echo postStart") {
            seen = append(seen, "postStart")
        }
    }
    want := []string{"onCreate", "postCreate", "postStart"}
    if !reflect.DeepEqual(seen, want) {
        t.Errorf("order = %v, want %v", seen, want)
    }
}
```

`newTestManagerWithFakeExec` is a test helper that returns a Manager whose runtime captures Exec calls in `fakeRT.Calls`.

- [ ] **Step 2: Run test to verify failure**

Run: `go test ./internal/run/ -run TestManager_DevcontainerLifecycleHooks -v`
Expected: FAIL.

- [ ] **Step 3: Invoke hooks after container start**

In `Manager.Create`, after the container is started and before `hooks.pre_run` runs:

```go
if useDC {
    // Probe the user's login-shell env so hooks see PATH, conda init, etc.
    rt := m.defaultRuntime()
    probedEnv, _ := devcontainer.ProbeUserEnv(ctx, rt, r.ContainerID, dcCfg.User)
    hookOpts := devcontainer.HookOpts{
        User:    dcCfg.User,
        Home:    dcCfg.Home,
        Workdir: workspaceTarget,
        Env:     probedEnv,
    }
    if err := devcontainer.RunHook(ctx, rt, r.ContainerID, "onCreate", dcCfg.OnCreateCmd, hookOpts, os.Stderr, os.Stderr); err != nil {
        return nil, fmt.Errorf("onCreateCommand failed: %w", err)
    }
    if err := devcontainer.RunHook(ctx, rt, r.ContainerID, "postCreate", dcCfg.PostCreateCmd, hookOpts, os.Stderr, os.Stderr); err != nil {
        return nil, fmt.Errorf("postCreateCommand failed: %w", err)
    }
    if err := devcontainer.RunHook(ctx, rt, r.ContainerID, "postStart", dcCfg.PostStartCmd, hookOpts, os.Stderr, os.Stderr); err != nil {
        ui.Warnf("postStartCommand failed: %v", err)
    }
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/run/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/run/
git commit -m "feat(run): run devcontainer lifecycle hooks after container start"
```

### Task 2.9: Re-run postStartCommand on Start

**Files:**
- Modify: `internal/run/manager.go` (find `Start` method)

- [ ] **Step 1: Locate the Start method**

Run: `grep -n "func .*Start" /workspace/internal/run/manager.go`

- [ ] **Step 2: Write failing test**

Append to `devcontainer_integration_test.go`:

```go
func TestManager_StartReRunsPostStart(t *testing.T) {
    workspace := t.TempDir()
    dcDir := filepath.Join(workspace, ".devcontainer")
    os.MkdirAll(dcDir, 0o755)
    os.WriteFile(filepath.Join(dcDir, "devcontainer.json"), []byte(`{
      "image": "ubuntu:24.04",
      "postStartCommand": "echo restarted"
    }`), 0o644)

    m, fakeRT := newTestManagerWithFakeExec(t)
    run, err := m.Create(context.Background(), CreateOptions{Workspace: workspace})
    if err != nil {
        t.Fatal(err)
    }
    fakeRT.Calls = nil // reset
    if err := m.Start(context.Background(), run.ID); err != nil {
        t.Fatal(err)
    }
    found := false
    for _, c := range fakeRT.Calls {
        if strings.Contains(c.Cmd, "echo restarted") {
            found = true
        }
    }
    if !found {
        t.Error("postStart did not re-run on Start")
    }
}
```

- [ ] **Step 3: Run test to verify failure**

Run: `go test ./internal/run/ -run TestManager_StartReRunsPostStart -v`
Expected: FAIL.

- [ ] **Step 4: Add postStart re-run inside Manager.Start**

Inside `Manager.Start`, after the container transitions to running:

```go
if r.PostStartCmd != "" {
    rt := m.defaultRuntime()
    env, _ := devcontainer.ProbeUserEnv(ctx, rt, r.ContainerID, r.PostStartUser)
    hookOpts := devcontainer.HookOpts{
        User:    r.PostStartUser,
        Home:    r.PostStartHome,
        Workdir: r.PostStartWorkdir,
        Env:     env,
    }
    if err := devcontainer.RunHook(ctx, rt, r.ContainerID, "postStart", r.PostStartCmd, hookOpts, os.Stderr, os.Stderr); err != nil {
        ui.Warnf("postStartCommand failed on restart: %v", err)
    }
}
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/run/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/run/
git commit -m "feat(run): re-run postStartCommand on container restart"
```

### Task 2.10: Manager-level regression sweep

- [ ] **Step 1: Run unit tests with race**

Run: `make test-unit`
Expected: PASS.

- [ ] **Step 2: Lint**

Run: `make lint`
Expected: clean.

- [ ] **Step 3: Smoke test locally (only if a runtime is available)**

In a separate workspace with a minimal `.devcontainer/devcontainer.json` (just `{"image": "alpine:3.19"}`), run:

```bash
moat run --agent claude --grant github -- echo hello
```

Verify:
- Stage A tag `moat-devcontainer-<basename>:base-...` exists (`docker images`).
- Container runs as root, mounted at `/workspaces/<basename>`.
- `echo hello` prints.

- [ ] **Step 4: Open PR 2**

```bash
gh pr create
```

Title: `feat(run): wire devcontainer.json into image and container setup`

---

## PR 3 — CLI, docs, and remaining E2E

### Task 3.1: `--no-devcontainer` CLI flag

**Files:**
- Modify: `cmd/moat/cli/run.go`

- [ ] **Step 1: Add the flag**

Find the `runCmd` (cobra) definition. Add:

```go
runCmd.Flags().Bool("no-devcontainer", false, "Ignore .devcontainer/devcontainer.json in the workspace")
```

In the run handler, read the flag and pass into `CreateOptions`:

```go
noDC, _ := cmd.Flags().GetBool("no-devcontainer")
opts.NoDevcontainer = noDC
```

- [ ] **Step 2: Add a CLI-level test**

Append to `cmd/moat/cli/run_test.go` (or create if absent):

```go
func TestRun_NoDevcontainerFlag(t *testing.T) {
    cmd := newRunCmd()
    if cmd.Flags().Lookup("no-devcontainer") == nil {
        t.Fatal("--no-devcontainer flag missing")
    }
}
```

- [ ] **Step 3: Run tests**

Run: `make test-unit ARGS='-run TestRun_NoDevcontainerFlag'`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add cmd/moat/cli/run.go cmd/moat/cli/run_test.go
git commit -m "feat(cli): add --no-devcontainer flag to moat run"
```

### Task 3.2: `moat init` detects devcontainer

**Files:**
- Modify: `cmd/moat/cli/init.go`
- Modify: `cmd/moat/cli/init_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestInit_DevcontainerDetected_OmitsBaseImageAndDeps(t *testing.T) {
    dir := t.TempDir()
    dcDir := filepath.Join(dir, ".devcontainer")
    os.MkdirAll(dcDir, 0o755)
    os.WriteFile(filepath.Join(dcDir, "devcontainer.json"), []byte(`{"image":"ubuntu:24.04"}`), 0o644)

    if err := runInit(dir, initOptions{Agent: "claude"}); err != nil {
        t.Fatalf("init: %v", err)
    }
    body, _ := os.ReadFile(filepath.Join(dir, "moat.yaml"))
    if strings.Contains(string(body), "base_image:") {
        t.Errorf("moat.yaml should NOT have base_image when devcontainer detected:\n%s", body)
    }
    if !strings.Contains(string(body), "# .devcontainer/devcontainer.json is used as the image source") {
        t.Errorf("moat.yaml should explain the devcontainer is the source of truth:\n%s", body)
    }
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `go test ./cmd/moat/cli/ -run TestInit_DevcontainerDetected -v`
Expected: FAIL.

- [ ] **Step 3: Add detection branch in `runInit`**

In `cmd/moat/cli/init.go`, when generating the moat.yaml template, call `devcontainer.Detect(workspace)`. If non-nil:

- Omit `base_image:` and `dependencies:` from the template entirely.
- Prepend the top of the file with:

```yaml
# .devcontainer/devcontainer.json is used as the image source for moat.
# Run `moat run --no-devcontainer` to bypass it.
```

- [ ] **Step 4: Run tests**

Run: `make test-unit ARGS='-run TestInit_DevcontainerDetected'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/moat/cli/init.go cmd/moat/cli/init_test.go
git commit -m "feat(cli): moat init detects devcontainer and writes minimal moat.yaml"
```

### Task 3.3: `moat doctor` adds a Devcontainer section

**Files:**
- Modify: `cmd/moat/cli/doctor.go`
- Modify: `cmd/moat/cli/doctor_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestDoctor_DevcontainerSection(t *testing.T) {
    dir := t.TempDir()
    dcDir := filepath.Join(dir, ".devcontainer")
    os.MkdirAll(dcDir, 0o755)
    os.WriteFile(filepath.Join(dcDir, "devcontainer.json"), []byte(`{
      "image": "ubuntu:24.04",
      "remoteUser": "vscode",
      "workspaceFolder": "/work/x"
    }`), 0o644)

    out := &bytes.Buffer{}
    if err := runDoctor(dir, out); err != nil {
        t.Fatal(err)
    }
    s := out.String()
    for _, want := range []string{"Devcontainer", "ubuntu:24.04", "vscode", "/work/x"} {
        if !strings.Contains(s, want) {
            t.Errorf("doctor output missing %q:\n%s", want, s)
        }
    }
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `make test-unit ARGS='-run TestDoctor_DevcontainerSection'`
Expected: FAIL.

- [ ] **Step 3: Add the section**

In `cmd/moat/cli/doctor.go`, alongside existing diagnostic sections, add:

```go
func reportDevcontainer(w io.Writer, workspace string, cfg *config.Config) {
    dc, err := devcontainer.Detect(workspace)
    if err != nil {
        fmt.Fprintf(w, "Devcontainer: ERROR parsing: %v\n", err)
        return
    }
    if dc == nil {
        fmt.Fprintln(w, "Devcontainer: not present")
        return
    }
    use := useDevcontainerForImage(cfg, dc)
    src := "image: " + dc.Image
    if dc.Build != nil {
        src = "build: " + dc.Build.Dockerfile
    }
    fmt.Fprintln(w, "Devcontainer:")
    fmt.Fprintf(w, "  source:           %s\n", src)
    fmt.Fprintf(w, "  user:             %s\n", dc.User)
    fmt.Fprintf(w, "  workspaceFolder:  %s\n", dc.WorkspaceFolder)
    fmt.Fprintf(w, "  used by moat:     %v\n", use)
}
```

Call from `runDoctor`.

- [ ] **Step 4: Run tests**

Run: `make test-unit ARGS='-run TestDoctor_DevcontainerSection'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/moat/cli/doctor.go cmd/moat/cli/doctor_test.go
git commit -m "feat(cli): moat doctor reports devcontainer config"
```

### Task 3.4: `moat status` shows devcontainer drift hint

**Files:**
- Modify: `cmd/moat/cli/status.go`
- Modify: `cmd/moat/cli/status_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestStatus_DevcontainerDriftHint(t *testing.T) {
    workspace := t.TempDir()
    dcDir := filepath.Join(workspace, ".devcontainer")
    os.MkdirAll(dcDir, 0o755)
    os.WriteFile(filepath.Join(dcDir, "devcontainer.json"), []byte(`{"image":"ubuntu:24.04"}`), 0o644)
    originalHash, _ := devcontainer.ContentHash(workspace)

    run := &Run{Workspace: workspace, DevcontainerHash: originalHash}
    out := &bytes.Buffer{}
    renderStatus(out, run)
    if strings.Contains(out.String(), "devcontainer.json changed") {
        t.Errorf("hint shown when hash matches:\n%s", out)
    }

    // Mutate the file.
    os.WriteFile(filepath.Join(dcDir, "devcontainer.json"), []byte(`{"image":"ubuntu:22.04"}`), 0o644)
    out.Reset()
    renderStatus(out, run)
    if !strings.Contains(out.String(), "devcontainer.json changed") {
        t.Errorf("hint missing after drift:\n%s", out)
    }
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `make test-unit ARGS='-run TestStatus_DevcontainerDriftHint'`
Expected: FAIL.

- [ ] **Step 3: Add drift check**

In `cmd/moat/cli/status.go`, alongside existing rendering:

```go
if r.DevcontainerHash != "" {
    cur, err := devcontainer.ContentHash(r.Workspace)
    if err == nil && cur != r.DevcontainerHash {
        fmt.Fprintln(w, "  hint: devcontainer.json changed; `moat run --rebuild` to apply")
    }
}
```

- [ ] **Step 4: Run tests**

Run: `make test-unit ARGS='-run TestStatus_DevcontainerDriftHint'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/moat/cli/status.go cmd/moat/cli/status_test.go
git commit -m "feat(cli): moat status surfaces devcontainer.json drift"
```

### Task 3.5: `--rebuild` also invalidates Stage A tag

**Files:**
- Modify: `internal/run/manager.go`

- [ ] **Step 1: Locate the existing --rebuild handler**

Search: `grep -n "opts.Rebuild" /workspace/internal/run/manager.go`

- [ ] **Step 2: Write failing test**

Append to `devcontainer_integration_test.go`:

```go
func TestManager_RebuildRemovesStageATag(t *testing.T) {
    workspace := t.TempDir()
    dcDir := filepath.Join(workspace, ".devcontainer")
    os.MkdirAll(dcDir, 0o755)
    os.WriteFile(filepath.Join(dcDir, "devcontainer.json"), []byte(`{"image":"ubuntu:24.04"}`), 0o644)

    m, fakeBM := newTestManagerWithFakeBuildManager(t)
    hash, _ := devcontainer.ContentHash(workspace)
    fakeTag := "moat-devcontainer-" + filepath.Base(workspace) + ":base-" + hash[:12]
    fakeBM.MarkExists(fakeTag)

    var removed []string
    fakeBM.OnRemove = func(tag string) { removed = append(removed, tag) }

    _, err := m.Create(context.Background(), CreateOptions{Workspace: workspace, Rebuild: true})
    if err != nil {
        t.Fatal(err)
    }
    found := false
    for _, t := range removed {
        if t == fakeTag {
            found = true
        }
    }
    if !found {
        t.Errorf("Stage A tag was not removed under --rebuild; removed = %v", removed)
    }
}
```

- [ ] **Step 3: Run test to verify failure**

Run: `make test-unit ARGS='-run TestManager_RebuildRemovesStageATag'`
Expected: FAIL.

- [ ] **Step 4: Extend the rebuild branch**

Where `opts.Rebuild` removes the overlay tag, also remove the Stage A tag:

```go
if opts.Rebuild && useDC {
    hash, err := devcontainer.ContentHash(opts.Workspace)
    if err == nil {
        baseTag := fmt.Sprintf("moat-devcontainer-%s:base-%s", filepath.Base(opts.Workspace), hash[:12])
        _ = m.defaultRuntime().RemoveImage(ctx, baseTag)
    }
}
```

- [ ] **Step 5: Run tests**

Run: `make test-unit ARGS='-run TestManager_RebuildRemovesStageATag'`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/run/manager.go internal/run/devcontainer_integration_test.go
git commit -m "feat(run): --rebuild invalidates devcontainer Stage A tag"
```

### Task 3.6: E2E — image-only devcontainer

**Files:**
- Create: `internal/e2e/devcontainer_test.go`
- Create: `internal/e2e/testdata/devcontainer/image-only/.devcontainer/devcontainer.json`

- [ ] **Step 1: Add fixture**

`internal/e2e/testdata/devcontainer/image-only/.devcontainer/devcontainer.json`:

```json
{
  "image": "mcr.microsoft.com/devcontainers/base:bookworm",
  "remoteUser": "vscode"
}
```

- [ ] **Step 2: Write the E2E test**

`internal/e2e/devcontainer_test.go`:

```go
//go:build e2e

package e2e

import (
    "context"
    "os/exec"
    "path/filepath"
    "strings"
    "testing"
)

func TestE2E_DevcontainerImageOnly(t *testing.T) {
    workspace := copyFixture(t, "devcontainer/image-only")
    cleanup := func() {
        exec.Command("moat", "destroy", "--all", "--workspace", workspace).Run()
    }
    t.Cleanup(cleanup)

    cmd := exec.Command("moat", "run", "--agent", "claude", "--workspace", workspace, "--", "id", "-u")
    out, err := cmd.CombinedOutput()
    if err != nil {
        t.Fatalf("moat run: %v\n%s", err, out)
    }
    // On Linux, UID should match the host workspace owner. On macOS, it
    // should still match (devcontainer base ships UID 1000 vscode but our
    // remap is skipped there — we just confirm the run succeeded).
    if !strings.Contains(string(out), "\n") {
        t.Errorf("unexpected output: %s", out)
    }

    cmd = exec.Command("moat", "exec", "--workspace", workspace, "--", "pwd")
    out, err = cmd.CombinedOutput()
    if err != nil {
        t.Fatalf("moat exec pwd: %v\n%s", err, out)
    }
    if !strings.Contains(string(out), "/workspaces/") {
        t.Errorf("workspace not mounted at /workspaces/<name>: %s", out)
    }
}

func copyFixture(t *testing.T, name string) string {
    t.Helper()
    dst := t.TempDir()
    src := filepath.Join("testdata", name)
    if err := exec.Command("cp", "-R", src+"/.", dst).Run(); err != nil {
        t.Fatal(err)
    }
    _ = context.Background
    return dst
}
```

- [ ] **Step 3: Run only on a runtime-enabled host**

Run: `go test -tags=e2e -v ./internal/e2e/ -run TestE2E_DevcontainerImageOnly`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/e2e/devcontainer_test.go internal/e2e/testdata/devcontainer/
git commit -m "test(e2e): devcontainer image-only scenario"
```

### Task 3.7: E2E — Dockerfile build

**Files:**
- Create: `internal/e2e/testdata/devcontainer/dockerfile-build/.devcontainer/devcontainer.json`
- Create: `internal/e2e/testdata/devcontainer/dockerfile-build/.devcontainer/Dockerfile`
- Modify: `internal/e2e/devcontainer_test.go`

- [ ] **Step 1: Add fixtures**

`devcontainer.json`:

```json
{
  "build": { "dockerfile": "Dockerfile" }
}
```

`Dockerfile`:

```dockerfile
FROM debian:bookworm-slim
RUN printf '#!/bin/sh\necho moat-was-here\n' > /usr/local/bin/moat-marker \
    && chmod +x /usr/local/bin/moat-marker
```

- [ ] **Step 2: Write the test**

Append to `internal/e2e/devcontainer_test.go`:

```go
func TestE2E_DevcontainerDockerfileBuild(t *testing.T) {
    workspace := copyFixture(t, "devcontainer/dockerfile-build")
    t.Cleanup(func() { exec.Command("moat", "destroy", "--all", "--workspace", workspace).Run() })

    cmd := exec.Command("moat", "run", "--agent", "claude", "--workspace", workspace, "--", "moat-marker")
    out, err := cmd.CombinedOutput()
    if err != nil {
        t.Fatalf("moat run: %v\n%s", err, out)
    }
    if !strings.Contains(string(out), "moat-was-here") {
        t.Errorf("custom binary missing from container: %s", out)
    }
}
```

- [ ] **Step 3: Run**

Run: `go test -tags=e2e -v ./internal/e2e/ -run TestE2E_DevcontainerDockerfileBuild`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/e2e/
git commit -m "test(e2e): devcontainer build.dockerfile scenario"
```

### Task 3.8: E2E — full-lifecycle markers

**Files:**
- Create: `internal/e2e/testdata/devcontainer/full-lifecycle/.devcontainer/devcontainer.json`
- Modify: `internal/e2e/devcontainer_test.go`

- [ ] **Step 1: Add fixture**

`devcontainer.json`:

```json
{
  "image": "debian:bookworm-slim",
  "initializeCommand": "touch ${localWorkspaceFolder}/initialize.host",
  "onCreateCommand":   "touch ${containerWorkspaceFolder}/onCreate.in",
  "postCreateCommand": "touch ${containerWorkspaceFolder}/postCreate.in",
  "postStartCommand":  "touch ${containerWorkspaceFolder}/postStart.in"
}
```

- [ ] **Step 2: Write test**

```go
func TestE2E_DevcontainerFullLifecycle(t *testing.T) {
    workspace := copyFixture(t, "devcontainer/full-lifecycle")
    t.Cleanup(func() { exec.Command("moat", "destroy", "--all", "--workspace", workspace).Run() })

    cmd := exec.Command("moat", "run", "--agent", "claude", "--workspace", workspace, "--", "true")
    out, err := cmd.CombinedOutput()
    if err != nil {
        t.Fatalf("moat run: %v\n%s", err, out)
    }
    markers := []string{"initialize.host", "onCreate.in", "postCreate.in", "postStart.in"}
    for _, m := range markers {
        if _, err := os.Stat(filepath.Join(workspace, m)); err != nil {
            t.Errorf("marker %s missing: %v", m, err)
        }
    }
}
```

- [ ] **Step 3: Run**

Run: `go test -tags=e2e -v ./internal/e2e/ -run TestE2E_DevcontainerFullLifecycle`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/e2e/
git commit -m "test(e2e): devcontainer full lifecycle hooks"
```

### Task 3.9: Documentation — CLI reference

**Files:**
- Modify: `docs/content/reference/01-cli.md`

- [ ] **Step 1: Add `--no-devcontainer` to the `moat run` section**

Find the `moat run` flag table. Add a row:

```
| --no-devcontainer | Ignore .devcontainer/devcontainer.json in the workspace |
```

Update the `--rebuild` description to mention that it also invalidates the devcontainer base image.

- [ ] **Step 2: Verify rendering locally if possible (optional)**

If a docs site preview is available, run it. Otherwise eyeball the markdown.

- [ ] **Step 3: Commit**

```bash
git add docs/content/reference/01-cli.md
git commit -m "docs(cli): document --no-devcontainer and --rebuild behavior"
```

### Task 3.10: Documentation — moat.yaml reference

**Files:**
- Modify: `docs/content/reference/02-moat-yaml.md`

- [ ] **Step 1: Add a precedence note under `base_image:`**

Add a paragraph:

> If a `.devcontainer/devcontainer.json` is present in the workspace,
> moat uses it as the source of truth for the image, user,
> `workspaceFolder`, environment, mounts, and lifecycle hooks —
> **unless** moat.yaml sets `base_image:` or `dependencies:`, in which
> case moat.yaml wins for the image and the devcontainer is ignored
> (a warning is printed at run time).

- [ ] **Step 2: Commit**

```bash
git add docs/content/reference/02-moat-yaml.md
git commit -m "docs(moat-yaml): document devcontainer precedence"
```

### Task 3.11: New guide — devcontainer.md

**Files:**
- Create: `docs/content/guides/06-devcontainer.md` (next available number)

- [ ] **Step 1: Write the guide**

Cover:

- When moat uses your devcontainer.json (precedence rule)
- What devcontainer.json fields moat honors and which it ignores
- A worked example: drop a devcontainer.json, run `moat run`
- The two-stage build model and how to read the resulting image tags
- Lifecycle hook order and failure semantics
- UID/GID remapping note for Linux users
- How to bypass with `--no-devcontainer`
- Pointer to the design doc (`docs/plans/2026-05-20-devcontainer-design.md`)

Keep it factual per `docs/STYLE-GUIDE.md`.

- [ ] **Step 2: Verify links**

Run: `grep -n "majorcontext.com/moat" docs/content/guides/06-devcontainer.md`
Confirm all external URLs follow the project URL structure.

- [ ] **Step 3: Commit**

```bash
git add docs/content/guides/06-devcontainer.md
git commit -m "docs(guides): add devcontainer.json usage guide"
```

### Task 3.12: CHANGELOG entry

**Files:**
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Add entry under "Unreleased" → "Added"**

```markdown
### Added

- **Devcontainer support** — Workspaces with a `.devcontainer/devcontainer.json`
  now run under moat without additional configuration. Moat uses the
  devcontainer's image, user, `workspaceFolder`, environment, mounts, and
  lifecycle hooks (`initializeCommand`, `onCreateCommand`, `postCreateCommand`,
  `postStartCommand`). Moat.yaml's `base_image:` and `dependencies:` continue
  to take precedence when present. See
  [guides/devcontainer](https://majorcontext.com/moat/guides/devcontainer)
  ([#XXX](https://github.com/majorcontext/moat/pull/XXX)).
```

Replace `#XXX` with the actual PR number once opened.

- [ ] **Step 2: Commit**

```bash
git add CHANGELOG.md
git commit -m "docs(changelog): announce devcontainer support"
```

### Task 3.13: Final lint + open PR 3

- [ ] **Step 1: Lint**

Run: `make lint`
Expected: clean.

- [ ] **Step 2: Run the entire test suite**

Run: `make test-unit && go test -tags=e2e ./internal/e2e/`
Expected: PASS.

- [ ] **Step 3: Open PR**

```bash
gh pr create
```

Title: `feat: --no-devcontainer flag, doctor/init/status integration, docs, E2E`

---

## Verification checklist (run before declaring done)

- [ ] `go test ./internal/devcontainer/ -race -v` passes.
- [ ] `make test-unit` passes (covers run/manager, deps, container changes).
- [ ] `go test -tags=e2e ./internal/e2e/` passes the three devcontainer scenarios.
- [ ] `make lint` is clean.
- [ ] Manual smoke: in a workspace with only `.devcontainer/devcontainer.json` (no moat.yaml), `moat run --agent claude --grant github -- echo ok` prints `ok` and runs as `remoteUser`.
- [ ] Manual smoke: in a workspace with both moat.yaml (`base_image: alpine:3.19`) and a devcontainer, run prints the precedence warning and uses alpine.
- [ ] Manual smoke: editing `.devcontainer/devcontainer.json` and running `moat status` shows the drift hint.
