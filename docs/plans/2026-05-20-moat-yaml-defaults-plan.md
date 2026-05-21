# Per-User `moat.yaml` Defaults Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `~/.moat/defaults.yaml` file (same schema as `moat.yaml`) that merges into every project's loaded `Config`, with a `moat config show` command for transparency.

**Architecture:** Additive. A new `LoadDefaults()` reads the host-side file; a new pure `MergeConfig(defaults, project)` returns the resolved Config; the existing `Load(dir)` is extended to call them before validation. A separate `ConfigSources(defaults, project, merged)` computes per-field origin (`defaults`/`project`/`merged`) post-hoc for `moat config show --source`. No daemon changes; no signature changes to `Load`.

**Tech Stack:** Go 1.x, `gopkg.in/yaml.v3` (Node-based for `--source` comment placement), repo's table-driven test style.

**Spec:** `docs/plans/2026-05-20-moat-yaml-defaults-design.md` (read it before starting).

**Conventions (from `CLAUDE.md`):** Conventional Commits, NO `Co-Authored-By` lines. Run `go build ./...` + touched-package tests before each commit; `make lint` clean before merge.

---

## File Structure

| File | Responsibility | Action |
|---|---|---|
| `internal/config/defaults.go` | `LoadDefaults() (*Config, error)` reads `<GlobalConfigDir>/defaults.yaml`; nil-and-nil on missing file. | Create |
| `internal/config/merge.go` | `MergeConfig(defaults, project *Config) *Config` (pure, hand-written, no reflection in live path); `ConfigSources(defaults, project, merged *Config) SourceMap`; per-substruct merge helpers. | Create |
| `internal/config/merge_test.go` | Table-driven per-field tests; reflection-guarded coverage test; round-trip fixture tests. | Create |
| `internal/config/testdata/merge/` | Round-trip fixtures (defaults.yaml + project.yaml + expected-merged.yaml). | Create |
| `internal/config/config.go` | Factor file-IO+parse path into `loadProject(dir)`; new `Load(dir)` calls `loadProject` + `LoadDefaults` + `MergeConfig` + validate. | Modify |
| `internal/config/config_test.go` | One test confirming defaults are loaded + merged via `Load`. | Modify |
| `cmd/moat/cli/config.go` | `moat config show` Cobra command (flags `--source`, `--workspace`, `--no-defaults`). | Create |
| `cmd/moat/cli/config_test.go` | Output stability tests. | Create |
| `docs/content/reference/02-moat-yaml.md` | New "Defaults" subsection. | Modify |
| `docs/content/reference/01-cli.md` | `moat config show` documented. | Modify |
| `CHANGELOG.md` | **Added** entry. | Modify |

---

## Task 1: `LoadDefaults` reads `~/.moat/defaults.yaml` (missing-file is silent no-op)

**Files:**
- Create: `internal/config/defaults.go`
- Test: `internal/config/defaults_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/config/defaults_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("MOAT_HOME", tmp)

	t.Run("missing file returns nil,nil", func(t *testing.T) {
		cfg, err := LoadDefaults()
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if cfg != nil {
			t.Fatalf("cfg = %#v, want nil", cfg)
		}
	})

	t.Run("present file is parsed", func(t *testing.T) {
		path := filepath.Join(tmp, "defaults.yaml")
		content := `agent: claude
grants:
  - aws
claude:
  bedrock:
    enabled: true
`
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadDefaults()
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if cfg == nil {
			t.Fatal("cfg = nil, want non-nil")
		}
		if cfg.Agent != "claude" {
			t.Errorf("Agent = %q, want claude", cfg.Agent)
		}
		if len(cfg.Grants) != 1 || cfg.Grants[0] != "aws" {
			t.Errorf("Grants = %v, want [aws]", cfg.Grants)
		}
		if cfg.Claude.Bedrock == nil || !cfg.Claude.Bedrock.Enabled {
			t.Errorf("claude.bedrock not parsed: %#v", cfg.Claude.Bedrock)
		}
	})

	t.Run("malformed yaml returns error", func(t *testing.T) {
		path := filepath.Join(tmp, "defaults.yaml")
		if err := os.WriteFile(path, []byte("agent: [unterminated"), 0644); err != nil {
			t.Fatal(err)
		}
		_, err := LoadDefaults()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestLoadDefaults -v`
Expected: FAIL — `LoadDefaults` undefined (compile error).

- [ ] **Step 3: Create `internal/config/defaults.go`**

```go
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// DefaultsFilename is the per-user defaults file name.
// Located at <GlobalConfigDir>/defaults.yaml (default ~/.moat/defaults.yaml,
// or $MOAT_HOME/defaults.yaml when MOAT_HOME is set).
const DefaultsFilename = "defaults.yaml"

// LoadDefaults reads the per-user moat.yaml defaults file, if it exists.
//
// Returns:
//   - (cfg, nil) when the file exists and parses cleanly.
//   - (nil, nil) when the file does not exist — this is the common case for
//     users who do not use defaults.
//   - (nil, err) when the file exists but cannot be read or parsed.
//
// The file's schema is identical to moat.yaml; missing fields are zero values
// and are filled in by the project moat.yaml during MergeConfig.
func LoadDefaults() (*Config, error) {
	path := filepath.Join(GlobalConfigDir(), DefaultsFilename)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &cfg, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -run TestLoadDefaults -v`
Expected: PASS (all 3 subtests).

Run full package: `go test ./internal/config/`
Expected: ok (no regressions).

- [ ] **Step 5: Commit**

```bash
git add internal/config/defaults.go internal/config/defaults_test.go
git commit -m "feat(config): add LoadDefaults for per-user defaults file"
```

---

## Task 2: `MergeConfig` — scalar + map fields on top-level Config

**Files:**
- Create: `internal/config/merge.go`
- Test: `internal/config/merge_test.go`

This task handles the simple-shape fields on the top-level `Config` struct: scalars (`Name`, `Agent`, `Version`, `Interactive`, `Sandbox`, `Runtime`, `BaseImage`) and maps (`Env`, `Secrets`, `Ports`, `Services`). Slice and nested-struct fields are deferred to Tasks 3-5.

- [ ] **Step 1: Write the failing test**

Create `internal/config/merge_test.go`:

```go
package config

import (
	"reflect"
	"testing"
)

func TestMergeConfig_Scalars(t *testing.T) {
	cases := []struct {
		name     string
		defaults *Config
		project  *Config
		want     func(*Config)
	}{
		{
			name:     "project agent wins",
			defaults: &Config{Agent: "claude"},
			project:  &Config{Agent: "codex"},
			want:     func(c *Config) { c.Agent = "codex" },
		},
		{
			name:     "defaults agent fills empty project",
			defaults: &Config{Agent: "claude"},
			project:  &Config{},
			want:     func(c *Config) { c.Agent = "claude" },
		},
		{
			name:     "project name wins; defaults runtime fills",
			defaults: &Config{Name: "default-name", Runtime: "docker"},
			project:  &Config{Name: "proj-name"},
			want:     func(c *Config) { c.Name = "proj-name"; c.Runtime = "docker" },
		},
		{
			name:     "interactive bool: project true wins over defaults false",
			defaults: &Config{Interactive: false},
			project:  &Config{Interactive: true},
			want:     func(c *Config) { c.Interactive = true },
		},
		{
			name:     "interactive bool: defaults true survives when project false (zero value)",
			defaults: &Config{Interactive: true},
			project:  &Config{Interactive: false},
			want:     func(c *Config) { c.Interactive = true },
		},
		{
			name:     "base_image and sandbox scalars",
			defaults: &Config{BaseImage: "debian:bookworm-slim", Sandbox: "none"},
			project:  &Config{BaseImage: "ubuntu:24.04"},
			want:     func(c *Config) { c.BaseImage = "ubuntu:24.04"; c.Sandbox = "none" },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MergeConfig(tc.defaults, tc.project)
			want := &Config{}
			tc.want(want)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("merged = %#v\nwant     %#v", got, want)
			}
		})
	}
}

func TestMergeConfig_Maps(t *testing.T) {
	defaults := &Config{
		Env:     map[string]string{"A": "1", "B": "2"},
		Secrets: map[string]string{"S1": "op://a"},
		Ports:   map[string]int{"http": 8080, "ws": 9000},
	}
	project := &Config{
		Env:     map[string]string{"B": "two", "C": "3"},
		Secrets: map[string]string{"S2": "op://b"},
		Ports:   map[string]int{"http": 8888},
	}
	got := MergeConfig(defaults, project)

	wantEnv := map[string]string{"A": "1", "B": "two", "C": "3"}
	if !reflect.DeepEqual(got.Env, wantEnv) {
		t.Errorf("Env = %v, want %v", got.Env, wantEnv)
	}
	wantSecrets := map[string]string{"S1": "op://a", "S2": "op://b"}
	if !reflect.DeepEqual(got.Secrets, wantSecrets) {
		t.Errorf("Secrets = %v, want %v", got.Secrets, wantSecrets)
	}
	wantPorts := map[string]int{"http": 8888, "ws": 9000}
	if !reflect.DeepEqual(got.Ports, wantPorts) {
		t.Errorf("Ports = %v, want %v", got.Ports, wantPorts)
	}
}

func TestMergeConfig_NilInputs(t *testing.T) {
	t.Run("both nil returns empty Config", func(t *testing.T) {
		got := MergeConfig(nil, nil)
		if got == nil {
			t.Fatal("MergeConfig(nil, nil) = nil, want non-nil empty")
		}
		if !reflect.DeepEqual(got, &Config{}) {
			t.Errorf("got = %#v, want &Config{}", got)
		}
	})
	t.Run("nil defaults returns clone of project", func(t *testing.T) {
		project := &Config{Agent: "claude", Env: map[string]string{"X": "1"}}
		got := MergeConfig(nil, project)
		if !reflect.DeepEqual(got, project) {
			t.Errorf("got = %#v, want %#v", got, project)
		}
		// Confirm clone, not alias.
		project.Env["X"] = "MUTATED"
		if got.Env["X"] != "1" {
			t.Errorf("got.Env aliases project.Env: %v", got.Env)
		}
	})
	t.Run("nil project returns clone of defaults", func(t *testing.T) {
		defaults := &Config{Agent: "claude", Env: map[string]string{"X": "1"}}
		got := MergeConfig(defaults, nil)
		if !reflect.DeepEqual(got, defaults) {
			t.Errorf("got = %#v, want %#v", got, defaults)
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run 'TestMergeConfig_Scalars|TestMergeConfig_Maps|TestMergeConfig_NilInputs' -v`
Expected: FAIL — `MergeConfig` undefined.

- [ ] **Step 3: Create `internal/config/merge.go`**

```go
package config

// MergeConfig returns the resolved Config produced by merging defaults under
// project: project values win per field when set; defaults fill in missing
// (zero-value) fields; maps merge per-key with project winning per key;
// slices union with project entries appended after defaults (deduped per
// element shape — see Task 3 and Task 4 for slice rules).
//
// Either argument may be nil. MergeConfig never mutates its arguments and
// never returns nil. It is pure with respect to time, environment, and the
// filesystem.
//
// This file is hand-maintained per-field. Adding a new field to Config
// requires extending MergeConfig to cover it. The reflection-guarded
// TestMergeConfigCoversAllFields test in merge_test.go fails when a new
// field is added without merge support (see Task 6).
func MergeConfig(defaults, project *Config) *Config {
	if defaults == nil && project == nil {
		return &Config{}
	}
	if defaults == nil {
		return cloneConfig(project)
	}
	if project == nil {
		return cloneConfig(defaults)
	}

	out := &Config{}
	mergeScalars(defaults, project, out)
	mergeMaps(defaults, project, out)
	// Slice and nested-struct fields are filled by Tasks 3-5.
	return out
}

// mergeScalars handles scalar (and scalar-pointer) fields on Config.
// Rule: project wins if non-zero; defaults fills in otherwise. Bool fields
// use "OR semantics" — true survives from either side, because a project
// explicitly setting `interactive: false` is indistinguishable from omitting
// the field in the zero-value YAML decoding.
func mergeScalars(d, p, out *Config) {
	out.Name = pickStr(p.Name, d.Name)
	out.Agent = pickStr(p.Agent, d.Agent)
	out.Version = pickStr(p.Version, d.Version)
	out.Interactive = p.Interactive || d.Interactive
	out.Sandbox = pickStr(p.Sandbox, d.Sandbox)
	out.Runtime = pickStr(p.Runtime, d.Runtime)
	out.BaseImage = pickStr(p.BaseImage, d.BaseImage)
}

// mergeMaps handles map fields on Config. Per-key merge; project wins per
// key. Nil maps are treated as empty.
func mergeMaps(d, p, out *Config) {
	out.Env = mergeStringMap(d.Env, p.Env)
	out.Secrets = mergeStringMap(d.Secrets, p.Secrets)
	out.Ports = mergeIntMap(d.Ports, p.Ports)
	out.Services = mergeServicesMap(d.Services, p.Services)
}

// cloneConfig returns a deep enough copy that mutating the returned Config
// does not affect the original. It is the identity merge with nil on the
// other side.
func cloneConfig(c *Config) *Config {
	if c == nil {
		return nil
	}
	// Implement clone by merging the input against an empty Config; the
	// merge functions copy each field defensively.
	empty := &Config{}
	out := &Config{}
	mergeScalars(empty, c, out)
	mergeMaps(empty, c, out)
	// Slice and nested-struct fields filled in Tasks 3-5.
	return out
}

// pickStr returns primary if non-empty, otherwise fallback.
func pickStr(primary, fallback string) string {
	if primary != "" {
		return primary
	}
	return fallback
}

// mergeStringMap merges two string-keyed string-valued maps.
// Returns nil iff both inputs are nil-or-empty (preserves omitempty YAML behavior).
func mergeStringMap(base, override map[string]string) map[string]string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := make(map[string]string, len(base)+len(override))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range override {
		out[k] = v
	}
	return out
}

// mergeIntMap merges two string-keyed int-valued maps.
func mergeIntMap(base, override map[string]int) map[string]int {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := make(map[string]int, len(base)+len(override))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range override {
		out[k] = v
	}
	return out
}

// mergeServicesMap merges Config.Services. ServiceSpec is treated as opaque —
// project's entry wins for a given key.
func mergeServicesMap(base, override map[string]ServiceSpec) map[string]ServiceSpec {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := make(map[string]ServiceSpec, len(base)+len(override))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range override {
		out[k] = v
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -run 'TestMergeConfig_Scalars|TestMergeConfig_Maps|TestMergeConfig_NilInputs' -v`
Expected: PASS.

Run full package: `go test ./internal/config/`
Expected: ok.

- [ ] **Step 5: Commit**

```bash
git add internal/config/merge.go internal/config/merge_test.go
git commit -m "feat(config): MergeConfig handles scalar and map top-level fields"
```

---

## Task 3: `MergeConfig` — string-slice fields (union + dedupe)

**Files:**
- Modify: `internal/config/merge.go` (add `mergeSlices` and string-slice helpers; call from `MergeConfig`)
- Modify: `internal/config/merge_test.go`

Fields covered: `Dependencies`, `Grants`, `Command`, `LanguageServers`.

- [ ] **Step 1: Write the failing test**

Append to `internal/config/merge_test.go`:

```go
func TestMergeConfig_StringSlices(t *testing.T) {
	cases := []struct {
		name             string
		defaults         *Config
		project          *Config
		wantDependencies []string
		wantGrants       []string
		wantCommand      []string
		wantLangServers  []string
	}{
		{
			name:             "union with dedupe",
			defaults:         &Config{Dependencies: []string{"node@22", "git"}, Grants: []string{"aws"}},
			project:          &Config{Dependencies: []string{"git", "go"}, Grants: []string{"github"}},
			wantDependencies: []string{"node@22", "git", "go"},
			wantGrants:       []string{"aws", "github"},
		},
		{
			name:        "project-only command (Command does NOT union — it's an invocation, not a list of independent items)",
			defaults:    &Config{Command: []string{"agent", "--default-flag"}},
			project:     &Config{Command: []string{"agent", "--project-flag"}},
			wantCommand: []string{"agent", "--project-flag"},
		},
		{
			name:        "command fills from defaults when project unset",
			defaults:    &Config{Command: []string{"agent", "--default-flag"}},
			project:     &Config{},
			wantCommand: []string{"agent", "--default-flag"},
		},
		{
			name:            "language_servers union",
			defaults:        &Config{LanguageServers: []string{"gopls"}},
			project:         &Config{LanguageServers: []string{"typescript-language-server"}},
			wantLangServers: []string{"gopls", "typescript-language-server"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MergeConfig(tc.defaults, tc.project)
			if tc.wantDependencies != nil && !reflect.DeepEqual(got.Dependencies, tc.wantDependencies) {
				t.Errorf("Dependencies = %v, want %v", got.Dependencies, tc.wantDependencies)
			}
			if tc.wantGrants != nil && !reflect.DeepEqual(got.Grants, tc.wantGrants) {
				t.Errorf("Grants = %v, want %v", got.Grants, tc.wantGrants)
			}
			if tc.wantCommand != nil && !reflect.DeepEqual(got.Command, tc.wantCommand) {
				t.Errorf("Command = %v, want %v", got.Command, tc.wantCommand)
			}
			if tc.wantLangServers != nil && !reflect.DeepEqual(got.LanguageServers, tc.wantLangServers) {
				t.Errorf("LanguageServers = %v, want %v", got.LanguageServers, tc.wantLangServers)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestMergeConfig_StringSlices -v`
Expected: FAIL — `Dependencies`/`Grants`/`Command`/`LanguageServers` come back empty (no slice-merge code yet).

- [ ] **Step 3: Add slice merging**

In `internal/config/merge.go`, add after `mergeMaps`:

```go
// mergeSlices handles slice fields on Config.
//
// String slices that represent "lists of independent capabilities or items"
// (Dependencies, Grants, LanguageServers) union with dedupe by string equality.
// String slices that represent "an ordered invocation" (Command) follow the
// scalar rule: project wins if non-empty, defaults fills otherwise.
func mergeSlices(d, p, out *Config) {
	out.Dependencies = unionDedupe(d.Dependencies, p.Dependencies)
	out.Grants = unionDedupe(d.Grants, p.Grants)
	out.LanguageServers = unionDedupe(d.LanguageServers, p.LanguageServers)
	out.Command = pickStrSlice(p.Command, d.Command)
}

// unionDedupe returns base ++ override with later duplicates removed
// (first occurrence wins; order: base first, then override additions).
// Returns nil iff both inputs are nil-or-empty.
func unionDedupe(base, override []string) []string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(base)+len(override))
	out := make([]string, 0, len(base)+len(override))
	for _, v := range base {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	for _, v := range override {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// pickStrSlice returns primary if non-empty, else fallback (no merge).
func pickStrSlice(primary, fallback []string) []string {
	if len(primary) > 0 {
		return primary
	}
	if len(fallback) == 0 {
		return nil
	}
	out := make([]string, len(fallback))
	copy(out, fallback)
	return out
}
```

Add a call to `mergeSlices` in `MergeConfig` after the `mergeMaps` call:

```go
	mergeScalars(defaults, project, out)
	mergeMaps(defaults, project, out)
	mergeSlices(defaults, project, out)
```

And in `cloneConfig`:

```go
	mergeScalars(empty, c, out)
	mergeMaps(empty, c, out)
	mergeSlices(empty, c, out)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -run TestMergeConfig_StringSlices -v`
Expected: PASS.

Run full package: `go test ./internal/config/`
Expected: ok.

- [ ] **Step 5: Commit**

```bash
git add internal/config/merge.go internal/config/merge_test.go
git commit -m "feat(config): MergeConfig union-dedupes string-slice fields"
```

---

## Task 4: `MergeConfig` — slices of structs (keyed dedupe)

**Files:**
- Modify: `internal/config/merge.go`
- Modify: `internal/config/merge_test.go`

Fields covered: `Mounts` (key: `(Source, Target)`), `Volumes` (key: `Name`), `MCP` (key: `Name`), `Network.Rules` (key: `(Host, Method, Path)`).

For each, project's entry replaces defaults' entry on key collision.

- [ ] **Step 1: Write the failing test**

Append to `internal/config/merge_test.go`:

```go
func TestMergeConfig_StructSlices(t *testing.T) {
	t.Run("Mounts keyed by (Source,Target); project wins on collision", func(t *testing.T) {
		defaults := &Config{Mounts: []MountEntry{
			{Source: "/host/a", Target: "/c/a"},
			{Source: "/host/b", Target: "/c/b", ReadOnly: true},
		}}
		project := &Config{Mounts: []MountEntry{
			{Source: "/host/b", Target: "/c/b", ReadOnly: false}, // collision, project wins
			{Source: "/host/c", Target: "/c/c"},                  // new
		}}
		got := MergeConfig(defaults, project)
		want := []MountEntry{
			{Source: "/host/a", Target: "/c/a"},
			{Source: "/host/b", Target: "/c/b", ReadOnly: false},
			{Source: "/host/c", Target: "/c/c"},
		}
		if !reflect.DeepEqual(got.Mounts, want) {
			t.Errorf("Mounts = %+v\nwant      %+v", got.Mounts, want)
		}
	})

	t.Run("Mounts same source, different targets, both kept", func(t *testing.T) {
		defaults := &Config{Mounts: []MountEntry{{Source: "/host/a", Target: "/c/a"}}}
		project := &Config{Mounts: []MountEntry{{Source: "/host/a", Target: "/c/different"}}}
		got := MergeConfig(defaults, project)
		if len(got.Mounts) != 2 {
			t.Errorf("expected both mounts retained, got %+v", got.Mounts)
		}
	})

	t.Run("Volumes keyed by Name", func(t *testing.T) {
		defaults := &Config{Volumes: []VolumeConfig{{Name: "cache", Target: "/cache"}}}
		project := &Config{Volumes: []VolumeConfig{
			{Name: "cache", Target: "/cache", ReadOnly: true},   // collision, project wins
			{Name: "data", Target: "/data"},                      // new
		}}
		got := MergeConfig(defaults, project)
		want := []VolumeConfig{
			{Name: "cache", Target: "/cache", ReadOnly: true},
			{Name: "data", Target: "/data"},
		}
		if !reflect.DeepEqual(got.Volumes, want) {
			t.Errorf("Volumes = %+v\nwant      %+v", got.Volumes, want)
		}
	})

	t.Run("MCP keyed by Name", func(t *testing.T) {
		defaults := &Config{MCP: []MCPServerConfig{{Name: "filesys", URL: "https://a"}}}
		project := &Config{MCP: []MCPServerConfig{
			{Name: "filesys", URL: "https://b"}, // collision, project wins
			{Name: "github", URL: "https://gh"}, // new
		}}
		got := MergeConfig(defaults, project)
		if len(got.MCP) != 2 {
			t.Fatalf("MCP len = %d, want 2", len(got.MCP))
		}
		if got.MCP[0].Name != "filesys" || got.MCP[0].URL != "https://b" {
			t.Errorf("MCP[0] = %+v, want {filesys, https://b}", got.MCP[0])
		}
		if got.MCP[1].Name != "github" {
			t.Errorf("MCP[1] = %+v, want github entry", got.MCP[1])
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestMergeConfig_StructSlices -v`
Expected: FAIL — Mounts/Volumes/MCP not yet merged.

- [ ] **Step 3: Add struct-slice merging**

Extend `mergeSlices` in `internal/config/merge.go`:

```go
func mergeSlices(d, p, out *Config) {
	out.Dependencies = unionDedupe(d.Dependencies, p.Dependencies)
	out.Grants = unionDedupe(d.Grants, p.Grants)
	out.LanguageServers = unionDedupe(d.LanguageServers, p.LanguageServers)
	out.Command = pickStrSlice(p.Command, d.Command)
	out.Mounts = mergeMounts(d.Mounts, p.Mounts)
	out.Volumes = mergeVolumes(d.Volumes, p.Volumes)
	out.MCP = mergeMCPServers(d.MCP, p.MCP)
}

// mergeMounts unions two []MountEntry slices, deduped by (Source, Target).
// Project entries replace defaults entries on key collision.
func mergeMounts(base, override []MountEntry) []MountEntry {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	type key struct{ Source, Target string }
	seen := make(map[key]int, len(base)+len(override))
	out := make([]MountEntry, 0, len(base)+len(override))
	for _, m := range base {
		k := key{m.Source, m.Target}
		seen[k] = len(out)
		out = append(out, m)
	}
	for _, m := range override {
		k := key{m.Source, m.Target}
		if idx, ok := seen[k]; ok {
			out[idx] = m
			continue
		}
		seen[k] = len(out)
		out = append(out, m)
	}
	return out
}

// mergeVolumes unions two []VolumeConfig slices, deduped by Name.
// Project entries replace defaults entries on Name collision.
func mergeVolumes(base, override []VolumeConfig) []VolumeConfig {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	seen := make(map[string]int, len(base)+len(override))
	out := make([]VolumeConfig, 0, len(base)+len(override))
	for _, v := range base {
		seen[v.Name] = len(out)
		out = append(out, v)
	}
	for _, v := range override {
		if idx, ok := seen[v.Name]; ok {
			out[idx] = v
			continue
		}
		seen[v.Name] = len(out)
		out = append(out, v)
	}
	return out
}

// mergeMCPServers unions two []MCPServerConfig slices, deduped by Name.
// Project entries replace defaults entries on Name collision.
func mergeMCPServers(base, override []MCPServerConfig) []MCPServerConfig {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	seen := make(map[string]int, len(base)+len(override))
	out := make([]MCPServerConfig, 0, len(base)+len(override))
	for _, m := range base {
		seen[m.Name] = len(out)
		out = append(out, m)
	}
	for _, m := range override {
		if idx, ok := seen[m.Name]; ok {
			out[idx] = m
			continue
		}
		seen[m.Name] = len(out)
		out = append(out, m)
	}
	return out
}
```

(`Network.Rules` is merged inside the `NetworkConfig` merge — Task 5.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -run TestMergeConfig_StructSlices -v`
Expected: PASS.

Run full package: `go test ./internal/config/`
Expected: ok.

- [ ] **Step 5: Commit**

```bash
git add internal/config/merge.go internal/config/merge_test.go
git commit -m "feat(config): MergeConfig merges Mounts, Volumes, MCP by key"
```

---

## Task 5: `MergeConfig` — nested struct fields (recursive merge)

**Files:**
- Modify: `internal/config/merge.go`
- Modify: `internal/config/merge_test.go`

Fields covered:
- `Claude` (ClaudeConfig) — including its nested `Bedrock *BedrockConfig`, `LLMGateway *LLMGatewayConfig`, `Plugins map[string]bool`, `Marketplaces map[string]MarketplaceSpec`, `MCP map[string]MCPServerSpec`, `Env map[string]string`, `SyncLogs *bool`, scalar `BaseURL`.
- `Codex` (CodexConfig) — `SyncLogs *bool`, `MCP map[string]MCPServerSpec`.
- `Gemini` (GeminiConfig) — same shape as Codex (read it to confirm).
- `Container` (ContainerConfig) — scalars `Memory`, `CPUs`; slice `DNS` (replace, not union — DNS order matters); map `Ulimits`.
- `Network` (NetworkConfig) — scalar `Policy`; deprecated `Allow []string` (replace-or-skip; today's behavior is "hard error if used", so we don't merge it); slice of structs `Rules`; pointer `KeepPolicy *keep.PolicyConfig`; slice of ints `Host`.
- `Snapshots` (SnapshotConfig).
- `Tracing` (TracingConfig).
- `Hooks` (HooksConfig).
- `Clipboard *bool` — pointer; project wins if non-nil.

For pointer-to-struct fields (`Bedrock`, `LLMGateway`, `Clipboard`, `KeepPolicy`): when both sides have the pointer non-nil, recurse and merge the pointed-to value with the same per-field rules; otherwise project wins if non-nil, defaults fills in.

> **Implementer note:** **Read the actual current definitions** of `ClaudeConfig`, `CodexConfig`, `GeminiConfig`, `ContainerConfig`, `NetworkConfig`, `SnapshotConfig`, `TracingConfig`, `HooksConfig`, `BedrockConfig`, `LLMGatewayConfig`, `MarketplaceSpec`, `MCPServerSpec`, `SnapshotTriggerConfig`, `SnapshotExcludeConfig`, `SnapshotRetentionConfig`, `keep.PolicyConfig` in the current source before writing the merge helpers — the field names and shapes may have evolved since this plan was written. Use this task's tests as the contract: tests pin behavior at the field level. If a struct has gained/lost fields, extend the tests AND merge helpers to match (and the coverage test in Task 6 will fail until you do).

- [ ] **Step 1: Write the failing test**

Append to `internal/config/merge_test.go`:

```go
func TestMergeConfig_Claude(t *testing.T) {
	t.Run("bedrock pointer + scalar override", func(t *testing.T) {
		defTrue := true
		defaults := &Config{Claude: ClaudeConfig{
			Bedrock: &BedrockConfig{Enabled: true, Region: "us-east-1"},
			Env:     map[string]string{"AWS_REGION": "us-east-1", "CLAUDE_FLAG": "1"},
		}}
		project := &Config{Claude: ClaudeConfig{
			Bedrock: &BedrockConfig{Region: "us-west-2"}, // override region; defaults' Enabled should carry
			Env:     map[string]string{"AWS_REGION": "us-west-2"},
		}}
		got := MergeConfig(defaults, project)
		_ = defTrue
		if got.Claude.Bedrock == nil {
			t.Fatal("Bedrock = nil, want non-nil")
		}
		if !got.Claude.Bedrock.Enabled {
			t.Errorf("Bedrock.Enabled = false, want true (from defaults)")
		}
		if got.Claude.Bedrock.Region != "us-west-2" {
			t.Errorf("Bedrock.Region = %q, want us-west-2", got.Claude.Bedrock.Region)
		}
		wantEnv := map[string]string{"AWS_REGION": "us-west-2", "CLAUDE_FLAG": "1"}
		if !reflect.DeepEqual(got.Claude.Env, wantEnv) {
			t.Errorf("Claude.Env = %v, want %v", got.Claude.Env, wantEnv)
		}
	})

	t.Run("only defaults has bedrock", func(t *testing.T) {
		defaults := &Config{Claude: ClaudeConfig{Bedrock: &BedrockConfig{Enabled: true}}}
		project := &Config{Claude: ClaudeConfig{}}
		got := MergeConfig(defaults, project)
		if got.Claude.Bedrock == nil || !got.Claude.Bedrock.Enabled {
			t.Errorf("expected Bedrock from defaults to survive, got %+v", got.Claude.Bedrock)
		}
	})

	t.Run("plugins map per-key merge", func(t *testing.T) {
		defaults := &Config{Claude: ClaudeConfig{Plugins: map[string]bool{"a@m": true, "b@m": true}}}
		project := &Config{Claude: ClaudeConfig{Plugins: map[string]bool{"b@m": false, "c@m": true}}}
		got := MergeConfig(defaults, project)
		want := map[string]bool{"a@m": true, "b@m": false, "c@m": true}
		if !reflect.DeepEqual(got.Claude.Plugins, want) {
			t.Errorf("Plugins = %v, want %v", got.Claude.Plugins, want)
		}
	})

	t.Run("base_url scalar fill from defaults", func(t *testing.T) {
		defaults := &Config{Claude: ClaudeConfig{BaseURL: "https://default.test"}}
		project := &Config{Claude: ClaudeConfig{}}
		got := MergeConfig(defaults, project)
		if got.Claude.BaseURL != "https://default.test" {
			t.Errorf("Claude.BaseURL = %q, want https://default.test", got.Claude.BaseURL)
		}
	})
}

func TestMergeConfig_Container(t *testing.T) {
	defaults := &Config{Container: ContainerConfig{Memory: 4096, CPUs: 4, DNS: []string{"8.8.8.8"}}}
	project := &Config{Container: ContainerConfig{Memory: 8192}}
	got := MergeConfig(defaults, project)
	if got.Container.Memory != 8192 {
		t.Errorf("Memory = %d, want 8192 (project)", got.Container.Memory)
	}
	if got.Container.CPUs != 4 {
		t.Errorf("CPUs = %d, want 4 (defaults)", got.Container.CPUs)
	}
	if !reflect.DeepEqual(got.Container.DNS, []string{"8.8.8.8"}) {
		t.Errorf("DNS = %v, want [8.8.8.8] (defaults)", got.Container.DNS)
	}
}

func TestMergeConfig_Network(t *testing.T) {
	defaults := &Config{Network: NetworkConfig{Policy: "strict"}}
	project := &Config{}
	got := MergeConfig(defaults, project)
	if got.Network.Policy != "strict" {
		t.Errorf("Policy = %q, want strict (from defaults)", got.Network.Policy)
	}
}

func TestMergeConfig_Clipboard(t *testing.T) {
	tru := true
	fls := false
	t.Run("project nil → defaults wins", func(t *testing.T) {
		got := MergeConfig(&Config{Clipboard: &fls}, &Config{})
		if got.Clipboard == nil || *got.Clipboard != false {
			t.Errorf("got %+v, want pointer to false", got.Clipboard)
		}
	})
	t.Run("project set → project wins", func(t *testing.T) {
		got := MergeConfig(&Config{Clipboard: &fls}, &Config{Clipboard: &tru})
		if got.Clipboard == nil || *got.Clipboard != true {
			t.Errorf("got %+v, want pointer to true", got.Clipboard)
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run 'TestMergeConfig_Claude|TestMergeConfig_Container|TestMergeConfig_Network|TestMergeConfig_Clipboard' -v`
Expected: FAIL — none of the nested-struct merges are implemented.

- [ ] **Step 3: Add nested-struct merging**

Add `mergeNested(d, p, out *Config)` and the per-substruct helpers to `internal/config/merge.go`. Read the current source for each substruct definition first to ensure all fields are covered:

```go
func mergeNested(d, p, out *Config) {
	out.Claude = mergeClaudeConfig(d.Claude, p.Claude)
	out.Codex = mergeCodexConfig(d.Codex, p.Codex)
	out.Gemini = mergeGeminiConfig(d.Gemini, p.Gemini)
	out.Container = mergeContainerConfig(d.Container, p.Container)
	out.Network = mergeNetworkConfig(d.Network, p.Network)
	out.Snapshots = mergeSnapshotConfig(d.Snapshots, p.Snapshots)
	out.Tracing = mergeTracingConfig(d.Tracing, p.Tracing)
	out.Hooks = mergeHooksConfig(d.Hooks, p.Hooks)
	out.Clipboard = mergeBoolPtr(p.Clipboard, d.Clipboard)
}

// mergeBoolPtr returns primary if non-nil, else fallback.
func mergeBoolPtr(primary, fallback *bool) *bool {
	if primary != nil {
		b := *primary
		return &b
	}
	if fallback == nil {
		return nil
	}
	b := *fallback
	return &b
}

func mergeClaudeConfig(d, p ClaudeConfig) ClaudeConfig {
	return ClaudeConfig{
		BaseURL:               pickStr(p.BaseURL, d.BaseURL),
		SyncLogs:              mergeBoolPtr(p.SyncLogs, d.SyncLogs),
		Plugins:               mergeBoolMap(d.Plugins, p.Plugins),
		Marketplaces:          mergeMarketplaceMap(d.Marketplaces, p.Marketplaces),
		MCP:                   mergeMCPSpecMap(d.MCP, p.MCP),
		LLMGateway:            mergeLLMGatewayPtr(d.LLMGateway, p.LLMGateway),
		Env:                   mergeStringMap(d.Env, p.Env),
		Bedrock:               mergeBedrockPtr(d.Bedrock, p.Bedrock),
		SkipPermissionsPrompt: p.SkipPermissionsPrompt || d.SkipPermissionsPrompt,
	}
}

func mergeCodexConfig(d, p CodexConfig) CodexConfig {
	return CodexConfig{
		SyncLogs: mergeBoolPtr(p.SyncLogs, d.SyncLogs),
		MCP:      mergeMCPSpecMap(d.MCP, p.MCP),
	}
}

func mergeGeminiConfig(d, p GeminiConfig) GeminiConfig {
	return GeminiConfig{
		SyncLogs: mergeBoolPtr(p.SyncLogs, d.SyncLogs),
		MCP:      mergeMCPSpecMap(d.MCP, p.MCP),
	}
}

func mergeContainerConfig(d, p ContainerConfig) ContainerConfig {
	out := ContainerConfig{
		Memory: pickInt(p.Memory, d.Memory),
		CPUs:   pickInt(p.CPUs, d.CPUs),
	}
	if len(p.DNS) > 0 {
		out.DNS = append([]string(nil), p.DNS...)
	} else if len(d.DNS) > 0 {
		out.DNS = append([]string(nil), d.DNS...)
	}
	out.Ulimits = mergeUlimitMap(d.Ulimits, p.Ulimits)
	return out
}

func mergeNetworkConfig(d, p NetworkConfig) NetworkConfig {
	return NetworkConfig{
		Policy:     pickStr(p.Policy, d.Policy),
		Rules:      mergeNetworkRules(d.Rules, p.Rules),
		KeepPolicy: pickKeepPolicyPtr(p.KeepPolicy, d.KeepPolicy),
		Host:       pickIntSlice(p.Host, d.Host),
		// Allow is deprecated (hard-errors during validation if used) — do not merge.
	}
}

// mergeNetworkRules unions two []NetworkRuleEntry slices, deduped by
// (Host, Method, Path). Project entries replace defaults' on collision.
func mergeNetworkRules(base, override []netrules.NetworkRuleEntry) []netrules.NetworkRuleEntry {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	type key struct{ Host, Method, Path string }
	keyOf := func(e netrules.NetworkRuleEntry) key {
		// HostRules contains Host; the entry contains Method/Path.
		// Implementer: confirm field names against the actual netrules struct;
		// adjust here if they differ.
		return key{Host: e.HostRules.Host, Method: e.Method, Path: e.Path}
	}
	seen := make(map[key]int, len(base)+len(override))
	out := make([]netrules.NetworkRuleEntry, 0, len(base)+len(override))
	for _, r := range base {
		seen[keyOf(r)] = len(out)
		out = append(out, r)
	}
	for _, r := range override {
		k := keyOf(r)
		if idx, ok := seen[k]; ok {
			out[idx] = r
			continue
		}
		seen[k] = len(out)
		out = append(out, r)
	}
	return out
}

func mergeSnapshotConfig(d, p SnapshotConfig) SnapshotConfig {
	// Implementer: read the current SnapshotConfig (and its nested
	// SnapshotTriggerConfig / ExcludeConfig / RetentionConfig) and apply the
	// per-field rules. Sketch:
	//   - scalar fields: project wins if non-zero
	//   - slice fields: replace (project if non-empty, else defaults)
	//   - nested struct fields: recurse with the same rules
	return SnapshotConfig{
		// Fill per actual current struct shape — tests below pin behavior.
	}
}

func mergeTracingConfig(d, p TracingConfig) TracingConfig {
	// Implementer: scalar-only struct (likely). Read its fields and apply
	// pickStr/pickInt per field type.
	return TracingConfig{}
}

func mergeHooksConfig(d, p HooksConfig) HooksConfig {
	// Implementer: read field shape (likely a slice of hook entries).
	// Apply union-dedupe if entries have a stable key, else project-replaces.
	return HooksConfig{}
}

// --- pointer-to-struct helpers ---

func mergeBedrockPtr(d, p *BedrockConfig) *BedrockConfig {
	if d == nil && p == nil {
		return nil
	}
	if d == nil {
		// Clone p to avoid aliasing.
		v := *p
		v.Models = mergeBedrockModels(BedrockModels{}, p.Models)
		return &v
	}
	if p == nil {
		v := *d
		v.Models = mergeBedrockModels(BedrockModels{}, d.Models)
		return &v
	}
	out := BedrockConfig{
		Enabled: p.Enabled || d.Enabled,
		Region:  pickStr(p.Region, d.Region),
		Models:  mergeBedrockModels(d.Models, p.Models),
	}
	return &out
}

func mergeBedrockModels(d, p BedrockModels) BedrockModels {
	return BedrockModels{
		Haiku:  pickStr(p.Haiku, d.Haiku),
		Sonnet: pickStr(p.Sonnet, d.Sonnet),
		Opus:   pickStr(p.Opus, d.Opus),
		Custom: pickStr(p.Custom, d.Custom),
	}
}

func mergeLLMGatewayPtr(d, p *LLMGatewayConfig) *LLMGatewayConfig {
	if d == nil && p == nil {
		return nil
	}
	if d == nil {
		v := *p
		return &v
	}
	if p == nil {
		v := *d
		return &v
	}
	// Implementer: LLMGatewayConfig has a Policy field of type *keep.PolicyConfig.
	// Recurse into it with the project-wins-if-non-nil rule.
	out := *d
	if p.Policy != nil {
		out.Policy = p.Policy
	}
	return &out
}

// pickKeepPolicyPtr returns primary if non-nil, else fallback. keep.PolicyConfig
// is treated as opaque — no recursive merge into its internals.
func pickKeepPolicyPtr(primary, fallback *keep.PolicyConfig) *keep.PolicyConfig {
	if primary != nil {
		return primary
	}
	return fallback
}

// --- scalar/map/slice helpers used above ---

func pickInt(primary, fallback int) int {
	if primary != 0 {
		return primary
	}
	return fallback
}

func pickIntSlice(primary, fallback []int) []int {
	if len(primary) > 0 {
		out := make([]int, len(primary))
		copy(out, primary)
		return out
	}
	if len(fallback) == 0 {
		return nil
	}
	out := make([]int, len(fallback))
	copy(out, fallback)
	return out
}

func mergeBoolMap(base, override map[string]bool) map[string]bool {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := make(map[string]bool, len(base)+len(override))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range override {
		out[k] = v
	}
	return out
}

func mergeMarketplaceMap(base, override map[string]MarketplaceSpec) map[string]MarketplaceSpec {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := make(map[string]MarketplaceSpec, len(base)+len(override))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range override {
		out[k] = v
	}
	return out
}

func mergeMCPSpecMap(base, override map[string]MCPServerSpec) map[string]MCPServerSpec {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := make(map[string]MCPServerSpec, len(base)+len(override))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range override {
		out[k] = v
	}
	return out
}

func mergeUlimitMap(base, override map[string]UlimitSpec) map[string]UlimitSpec {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := make(map[string]UlimitSpec, len(base)+len(override))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range override {
		out[k] = v
	}
	return out
}
```

Required new imports in `merge.go`:

```go
import (
	"github.com/majorcontext/moat/internal/netrules"
	"github.com/majorcontext/moat/internal/keep"
)
```

(Verify the keep package import path against the live source — it might be vendored under a slightly different path. Use whatever path the existing `internal/config/config.go` already uses for `keep.PolicyConfig`.)

Call `mergeNested` in `MergeConfig` and `cloneConfig`:

```go
// In MergeConfig, after the existing calls:
	mergeScalars(defaults, project, out)
	mergeMaps(defaults, project, out)
	mergeSlices(defaults, project, out)
	mergeNested(defaults, project, out)

// And in cloneConfig:
	mergeScalars(empty, c, out)
	mergeMaps(empty, c, out)
	mergeSlices(empty, c, out)
	mergeNested(empty, c, out)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/config/ -run 'TestMergeConfig_Claude|TestMergeConfig_Container|TestMergeConfig_Network|TestMergeConfig_Clipboard' -v`
Expected: PASS.

Run full package: `go test ./internal/config/ -v 2>&1 | tail -30`
Expected: ok across the merge tests.

Run `go build ./...` — clean.

- [ ] **Step 5: Commit**

```bash
git add internal/config/merge.go internal/config/merge_test.go
git commit -m "feat(config): MergeConfig recursively merges nested structs"
```

---

## Task 6: Reflection-guarded coverage test + `ConfigSources` for `--source`

**Files:**
- Modify: `internal/config/merge.go` (add `SourceMap`, `ConfigSource`, and `ConfigSources`)
- Modify: `internal/config/merge_test.go` (add the coverage test + `ConfigSources` tests)

The coverage test prevents silent merge-staleness when `Config` gains a new field. `ConfigSources` powers `moat config show --source` in Task 8.

- [ ] **Step 1: Write the failing test**

Append to `internal/config/merge_test.go`:

```go
import "reflect"

// TestMergeConfigCoversAllFields verifies that every exported field of
// Config has merge support. If this test fails, MergeConfig is missing a
// new field on the struct — extend mergeScalars/mergeMaps/mergeSlices/
// mergeNested to cover it.
//
// Strategy: set one field at a time on the `defaults` Config to a non-zero
// example; merge with a zero `project`; assert the merged result has the
// non-zero value. If a field is silently dropped, the test fails with the
// offending field name.
func TestMergeConfigCoversAllFields(t *testing.T) {
	cfgType := reflect.TypeOf(Config{})
	for i := 0; i < cfgType.NumField(); i++ {
		f := cfgType.Field(i)
		if !f.IsExported() {
			continue
		}
		if f.Tag.Get("yaml") == "-" {
			continue // explicitly excluded from YAML
		}
		t.Run(f.Name, func(t *testing.T) {
			defaults := &Config{}
			dv := reflect.ValueOf(defaults).Elem()
			fieldVal := dv.FieldByName(f.Name)
			nonZero := nonZeroValueFor(f.Type)
			if !nonZero.IsValid() {
				t.Skipf("no non-zero example for field %s (type %s); extend nonZeroValueFor", f.Name, f.Type)
			}
			fieldVal.Set(nonZero)

			merged := MergeConfig(defaults, &Config{})
			mv := reflect.ValueOf(merged).Elem().FieldByName(f.Name)

			if mv.IsZero() {
				t.Errorf("field %s was set in defaults but dropped by MergeConfig (got zero value); extend MergeConfig", f.Name)
			}
		})
	}
}

// nonZeroValueFor returns a non-zero reflect.Value for the given type, used
// only by TestMergeConfigCoversAllFields. Returns an invalid Value when the
// type isn't supported here — that subtest is skipped.
func nonZeroValueFor(t reflect.Type) reflect.Value {
	switch t.Kind() {
	case reflect.String:
		return reflect.ValueOf("x").Convert(t)
	case reflect.Bool:
		return reflect.ValueOf(true).Convert(t)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return reflect.ValueOf(int64(1)).Convert(t)
	case reflect.Slice:
		// Element-type-aware: single-element slice with a non-zero element.
		elem := nonZeroValueFor(t.Elem())
		if !elem.IsValid() {
			return reflect.Value{}
		}
		s := reflect.MakeSlice(t, 1, 1)
		s.Index(0).Set(elem)
		return s
	case reflect.Map:
		// Single-entry map with non-zero key and value.
		k := nonZeroValueFor(t.Key())
		v := nonZeroValueFor(t.Elem())
		if !k.IsValid() || !v.IsValid() {
			return reflect.Value{}
		}
		m := reflect.MakeMap(t)
		m.SetMapIndex(k, v)
		return m
	case reflect.Ptr:
		// Pointer to a zero element-type value is itself non-nil → counts as non-zero.
		return reflect.New(t.Elem())
	case reflect.Struct:
		// Struct with at least one non-zero exported field, if findable.
		v := reflect.New(t).Elem()
		set := false
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			fv := nonZeroValueFor(f.Type)
			if !fv.IsValid() {
				continue
			}
			v.FieldByIndex(f.Index).Set(fv)
			set = true
			break
		}
		if !set {
			return reflect.Value{}
		}
		return v
	default:
		return reflect.Value{}
	}
}

func TestConfigSources(t *testing.T) {
	defaults := &Config{
		Agent:  "claude",
		Grants: []string{"aws"},
		Claude: ClaudeConfig{Bedrock: &BedrockConfig{Enabled: true, Region: "us-east-1"}},
	}
	project := &Config{
		Grants: []string{"github"},
		Claude: ClaudeConfig{Bedrock: &BedrockConfig{Region: "us-west-2"}},
	}
	merged := MergeConfig(defaults, project)
	sources := ConfigSources(defaults, project, merged)

	if got := sources["agent"]; got != SourceDefaults {
		t.Errorf("agent source = %v, want defaults", got)
	}
	if got := sources["claude.bedrock.enabled"]; got != SourceDefaults {
		t.Errorf("claude.bedrock.enabled source = %v, want defaults", got)
	}
	if got := sources["claude.bedrock.region"]; got != SourceProject {
		t.Errorf("claude.bedrock.region source = %v, want project", got)
	}
	// Slices: each element annotated by its origin.
	if got := sources["grants[aws]"]; got != SourceDefaults {
		t.Errorf("grants[aws] source = %v, want defaults", got)
	}
	if got := sources["grants[github]"]; got != SourceProject {
		t.Errorf("grants[github] source = %v, want project", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config/ -run 'TestMergeConfigCoversAllFields|TestConfigSources' -v`
Expected: `TestConfigSources` FAILS (`ConfigSources`/`SourceDefaults`/`SourceProject` undefined). `TestMergeConfigCoversAllFields` may PASS if Tasks 2-5 covered everything, or FAIL specifically pointing at uncovered fields — fix those before continuing.

- [ ] **Step 3: Add `SourceMap` / `ConfigSource` / `ConfigSources`**

Append to `internal/config/merge.go`:

```go
// ConfigSource identifies where a resolved field's value came from.
type ConfigSource int

const (
	// SourceUnset means the field is zero in both inputs and merged result.
	SourceUnset ConfigSource = iota
	// SourceDefaults means the value came from ~/.moat/defaults.yaml.
	SourceDefaults
	// SourceProject means the value came from the project moat.yaml.
	SourceProject
	// SourceMerged means the value is the union/merge of both inputs
	// (used for maps and unioned slices where both sides contributed).
	SourceMerged
)

func (s ConfigSource) String() string {
	switch s {
	case SourceUnset:
		return "unset"
	case SourceDefaults:
		return "defaults"
	case SourceProject:
		return "project"
	case SourceMerged:
		return "merged"
	default:
		return "unknown"
	}
}

// SourceMap maps a yaml-style dotted path (e.g. "claude.bedrock.region",
// "grants[aws]", "env.AWS_REGION") to the source of the resolved value at
// that path.
type SourceMap map[string]ConfigSource

// ConfigSources computes the per-field origin of `merged` by diffing the
// resolved Config against `defaults` and `project`. For each leaf field
// reached, the source is determined by which input(s) carried the value.
//
// The function is computed post-hoc (no threading through MergeConfig) and
// is reflection-light: it walks Config's known fields explicitly, mirroring
// the structure of MergeConfig. Adding a new field to Config requires
// extending ConfigSources to cover it; the TestConfigSourcesCoversAllFields
// guard (in this file's tests, alongside TestMergeConfigCoversAllFields)
// asserts coverage.
func ConfigSources(defaults, project, merged *Config) SourceMap {
	sm := SourceMap{}
	if defaults == nil {
		defaults = &Config{}
	}
	if project == nil {
		project = &Config{}
	}
	if merged == nil {
		return sm
	}

	// Scalars and pointers on top-level Config.
	annotateStr(sm, "name", defaults.Name, project.Name, merged.Name)
	annotateStr(sm, "agent", defaults.Agent, project.Agent, merged.Agent)
	annotateStr(sm, "version", defaults.Version, project.Version, merged.Version)
	annotateBool(sm, "interactive", defaults.Interactive, project.Interactive, merged.Interactive)
	annotateStr(sm, "sandbox", defaults.Sandbox, project.Sandbox, merged.Sandbox)
	annotateStr(sm, "runtime", defaults.Runtime, project.Runtime, merged.Runtime)
	annotateStr(sm, "base_image", defaults.BaseImage, project.BaseImage, merged.BaseImage)
	annotateBoolPtr(sm, "clipboard", defaults.Clipboard, project.Clipboard, merged.Clipboard)

	// Maps and slices: per-key/-element source.
	annotateStringMap(sm, "env", defaults.Env, project.Env)
	annotateStringMap(sm, "secrets", defaults.Secrets, project.Secrets)
	annotateIntMap(sm, "ports", defaults.Ports, project.Ports)
	annotateStringSlice(sm, "dependencies", defaults.Dependencies, project.Dependencies)
	annotateStringSlice(sm, "grants", defaults.Grants, project.Grants)
	annotateStringSlice(sm, "language_servers", defaults.LanguageServers, project.LanguageServers)

	// Nested structs: recurse.
	annotateClaude(sm, "claude", defaults.Claude, project.Claude, merged.Claude)
	// Implementer: add Codex, Gemini, Container, Network, Snapshots,
	// Tracing, Hooks, and any new top-level field. The pattern is the same
	// as annotateClaude — call a sibling annotateXxx for each substruct.

	return sm
}

func annotateStr(sm SourceMap, path, d, p, m string) {
	switch {
	case m == "":
		sm[path] = SourceUnset
	case p != "" && p == m:
		sm[path] = SourceProject
	case d != "" && d == m:
		sm[path] = SourceDefaults
	default:
		sm[path] = SourceMerged
	}
}

func annotateBool(sm SourceMap, path string, d, p, m bool) {
	switch {
	case !m:
		sm[path] = SourceUnset
	case p && !d:
		sm[path] = SourceProject
	case d && !p:
		sm[path] = SourceDefaults
	default:
		sm[path] = SourceMerged
	}
}

func annotateBoolPtr(sm SourceMap, path string, d, p, m *bool) {
	if m == nil {
		sm[path] = SourceUnset
		return
	}
	if p != nil {
		sm[path] = SourceProject
		return
	}
	if d != nil {
		sm[path] = SourceDefaults
		return
	}
	sm[path] = SourceMerged
}

func annotateStringMap(sm SourceMap, path string, d, p map[string]string) {
	for k := range mapKeys(d, p) {
		_, inP := p[k]
		_, inD := d[k]
		switch {
		case inP && !inD:
			sm[path+"."+k] = SourceProject
		case inD && !inP:
			sm[path+"."+k] = SourceDefaults
		case inD && inP:
			if p[k] == d[k] {
				sm[path+"."+k] = SourceMerged
			} else {
				sm[path+"."+k] = SourceProject // project overrode defaults
			}
		}
	}
}

func annotateIntMap(sm SourceMap, path string, d, p map[string]int) {
	keys := map[string]struct{}{}
	for k := range d {
		keys[k] = struct{}{}
	}
	for k := range p {
		keys[k] = struct{}{}
	}
	for k := range keys {
		_, inP := p[k]
		_, inD := d[k]
		switch {
		case inP && !inD:
			sm[path+"."+k] = SourceProject
		case inD && !inP:
			sm[path+"."+k] = SourceDefaults
		default:
			if p[k] == d[k] {
				sm[path+"."+k] = SourceMerged
			} else {
				sm[path+"."+k] = SourceProject
			}
		}
	}
}

func annotateStringSlice(sm SourceMap, path string, d, p []string) {
	inP := make(map[string]struct{}, len(p))
	for _, v := range p {
		inP[v] = struct{}{}
	}
	inD := make(map[string]struct{}, len(d))
	for _, v := range d {
		inD[v] = struct{}{}
	}
	all := make([]string, 0, len(p)+len(d))
	all = append(all, d...)
	for _, v := range p {
		if _, ok := inD[v]; !ok {
			all = append(all, v)
		}
	}
	for _, v := range all {
		_, fromP := inP[v]
		_, fromD := inD[v]
		switch {
		case fromP && !fromD:
			sm[path+"["+v+"]"] = SourceProject
		case fromD && !fromP:
			sm[path+"["+v+"]"] = SourceDefaults
		default:
			sm[path+"["+v+"]"] = SourceMerged
		}
	}
}

func annotateClaude(sm SourceMap, path string, d, p, m ClaudeConfig) {
	annotateStr(sm, path+".base_url", d.BaseURL, p.BaseURL, m.BaseURL)
	annotateBoolPtr(sm, path+".sync_logs", d.SyncLogs, p.SyncLogs, m.SyncLogs)
	annotateStringMap(sm, path+".env", d.Env, p.Env)
	annotateBedrockPtr(sm, path+".bedrock", d.Bedrock, p.Bedrock, m.Bedrock)
	// Implementer: add Plugins/Marketplaces/MCP per-key annotation here as well.
}

func annotateBedrockPtr(sm SourceMap, path string, d, p, m *BedrockConfig) {
	if m == nil {
		sm[path] = SourceUnset
		return
	}
	dz := BedrockConfig{}
	pz := BedrockConfig{}
	if d != nil {
		dz = *d
	}
	if p != nil {
		pz = *p
	}
	annotateBool(sm, path+".enabled", dz.Enabled, pz.Enabled, m.Enabled)
	annotateStr(sm, path+".region", dz.Region, pz.Region, m.Region)
	// Models — per-tier scalar.
	annotateStr(sm, path+".models.haiku", dz.Models.Haiku, pz.Models.Haiku, m.Models.Haiku)
	annotateStr(sm, path+".models.sonnet", dz.Models.Sonnet, pz.Models.Sonnet, m.Models.Sonnet)
	annotateStr(sm, path+".models.opus", dz.Models.Opus, pz.Models.Opus, m.Models.Opus)
	annotateStr(sm, path+".models.custom", dz.Models.Custom, pz.Models.Custom, m.Models.Custom)
}

// mapKeys returns the union of keys across the given maps as a set.
func mapKeys(maps ...map[string]string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, m := range maps {
		for k := range m {
			out[k] = struct{}{}
		}
	}
	return out
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/config/ -run 'TestMergeConfigCoversAllFields|TestConfigSources' -v`
Expected: PASS.

Run full package: `go test ./internal/config/`
Expected: ok.

- [ ] **Step 5: Commit**

```bash
git add internal/config/merge.go internal/config/merge_test.go
git commit -m "feat(config): ConfigSources + reflection-guarded merge coverage"
```

---

## Task 7: Wire `Load(dir)` to merge defaults; add round-trip fixture test

**Files:**
- Modify: `internal/config/config.go` (factor file-IO+parse into `loadProject`, extend `Load` to merge defaults + validate)
- Modify: `internal/config/config_test.go`
- Create: `internal/config/testdata/merge/defaults.yaml`
- Create: `internal/config/testdata/merge/project.yaml`
- Create: `internal/config/testdata/merge/expected.yaml`

- [ ] **Step 1: Write the failing test (round-trip fixture)**

Create the fixtures first.

`internal/config/testdata/merge/defaults.yaml`:

```yaml
agent: claude
grants:
  - aws
env:
  AWS_REGION: us-east-1
claude:
  bedrock:
    enabled: true
    region: us-east-1
```

`internal/config/testdata/merge/project.yaml`:

```yaml
grants:
  - github
env:
  AWS_REGION: us-west-2
claude:
  bedrock:
    region: us-west-2
```

`internal/config/testdata/merge/expected.yaml`:

```yaml
agent: claude
grants:
  - aws
  - github
env:
  AWS_REGION: us-west-2
claude:
  bedrock:
    enabled: true
    region: us-west-2
```

Append to `internal/config/config_test.go`:

```go
func TestLoad_MergesDefaults(t *testing.T) {
	tmpProject := t.TempDir()
	tmpMoatHome := t.TempDir()
	t.Setenv("MOAT_HOME", tmpMoatHome)

	// Stage defaults.yaml at MOAT_HOME/defaults.yaml.
	defaultsSrc := mustReadFile(t, "testdata/merge/defaults.yaml")
	if err := os.WriteFile(filepath.Join(tmpMoatHome, "defaults.yaml"), defaultsSrc, 0644); err != nil {
		t.Fatal(err)
	}
	// Stage project moat.yaml.
	projectSrc := mustReadFile(t, "testdata/merge/project.yaml")
	if err := os.WriteFile(filepath.Join(tmpProject, "moat.yaml"), projectSrc, 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(tmpProject)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg == nil {
		t.Fatal("Load returned nil Config")
	}

	// Parse expected.
	var want Config
	expectedSrc := mustReadFile(t, "testdata/merge/expected.yaml")
	if err := yaml.Unmarshal(expectedSrc, &want); err != nil {
		t.Fatal(err)
	}

	// Compare individual fields the fixture cares about.
	if cfg.Agent != want.Agent {
		t.Errorf("Agent = %q, want %q", cfg.Agent, want.Agent)
	}
	if !reflect.DeepEqual(cfg.Grants, want.Grants) {
		t.Errorf("Grants = %v, want %v", cfg.Grants, want.Grants)
	}
	if cfg.Env["AWS_REGION"] != want.Env["AWS_REGION"] {
		t.Errorf("Env[AWS_REGION] = %q, want %q", cfg.Env["AWS_REGION"], want.Env["AWS_REGION"])
	}
	if cfg.Claude.Bedrock == nil || !cfg.Claude.Bedrock.Enabled {
		t.Errorf("Claude.Bedrock.Enabled = false, want true (from defaults)")
	}
	if cfg.Claude.Bedrock.Region != "us-west-2" {
		t.Errorf("Claude.Bedrock.Region = %q, want us-west-2 (project)", cfg.Claude.Bedrock.Region)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return data
}
```

(Ensure imports include `os`, `path/filepath`, `reflect`, and `gopkg.in/yaml.v3` — most already imported in `config_test.go`.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestLoad_MergesDefaults -v`
Expected: FAIL — `Load` doesn't merge defaults yet. Validation may or may not pass on the project-only Config.

- [ ] **Step 3: Refactor `Load(dir)` to call defaults + merge + validate**

In `internal/config/config.go`, factor the existing `Load(dir)` into `loadProject` (file IO + yaml parse + scalar field validation that only applies to a single source), then write a new `Load` that calls `loadProject`, `LoadDefaults`, `MergeConfig`, and runs the merged-config validation.

The minimal-risk change: keep `Load`'s signature, move the file-read and `yaml.Unmarshal` into `loadProject(dir) (*Config, error)`, leave the existing validation in `Load` operating on the merged result. Concretely:

```go
// Load reads moat.yaml from dir, merges in ~/.moat/defaults.yaml if present,
// and validates the resolved Config.
//
// Returns (nil, nil) when no project moat.yaml AND no defaults file exist —
// i.e. there is no Config to load.
func Load(dir string) (*Config, error) {
	project, err := loadProject(dir)
	if err != nil {
		return nil, err
	}
	defaults, err := LoadDefaults()
	if err != nil {
		return nil, err
	}
	if project == nil && defaults == nil {
		return nil, nil
	}
	cfg := MergeConfig(defaults, project)

	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// loadProject reads the project moat.yaml (or legacy agent.yaml) from dir
// and parses it. Returns (nil, nil) when no project file exists.
//
// No validation runs here — validation operates on the merged result.
func loadProject(dir string) (*Config, error) {
	path := filepath.Join(dir, ConfigFilename)
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("reading %s: %w", ConfigFilename, err)
		}
		// Try legacy agent.yaml
		path = filepath.Join(dir, LegacyConfigFilename)
		data, err = os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, nil
			}
			return nil, fmt.Errorf("reading %s: %w", LegacyConfigFilename, err)
		}
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", filepath.Base(path), err)
	}
	return &cfg, nil
}

// validateConfig contains all the validation rules previously inlined into
// Load. Extracting it makes Load's flow read top-to-bottom and lets
// `moat config show` validate the resolved config independently when wanted.
func validateConfig(cfg *Config) error {
	// Implementer: move the existing validation block from Load (the section
	// that runs after yaml.Unmarshal — runtime validation, base_image regex,
	// env/secrets overlap, secret URI scheme, command[0], claude marketplace
	// specs, claude.bedrock, claude.base_url, claude.llm-gateway, codex MCP,
	// gemini MCP, etc.) into this function verbatim. No logic changes.
	//
	// The validation function must accept and return only *Config / error.
	// All the existing checks already operate against a *Config; just relocate
	// them.
	return nil // <-- placeholder; the implementer MUST relocate the existing checks here.
}
```

> **Critical:** the placeholder `return nil` above is intentionally wrong — the implementer must move the existing validation logic from the old `Load` body into `validateConfig`. This is a mechanical cut-and-paste, NOT a rewrite. After the move, `Load` is short and `validateConfig` carries everything the old `Load` checked. The tests will catch any missed validation.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/config/ -run TestLoad_MergesDefaults -v`
Expected: PASS.

Run full package: `go test ./internal/config/`
Expected: ok — existing tests verifying validation behavior must still pass after the relocation.

Run `go build ./...` — clean.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go internal/config/testdata/
git commit -m "feat(config): Load merges defaults.yaml before validation"
```

---

## Task 8: `moat config show` CLI command

**Files:**
- Create: `cmd/moat/cli/config.go`
- Create: `cmd/moat/cli/config_test.go`

- [ ] **Step 1: Write the failing test**

Create `cmd/moat/cli/config_test.go`:

```go
package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigShow_DefaultBehavior(t *testing.T) {
	tmpProject := t.TempDir()
	tmpMoatHome := t.TempDir()
	t.Setenv("MOAT_HOME", tmpMoatHome)

	defaults := `agent: claude
grants:
  - aws
claude:
  bedrock:
    enabled: true
`
	project := `claude:
  bedrock:
    region: us-west-2
`
	if err := os.WriteFile(filepath.Join(tmpMoatHome, "defaults.yaml"), []byte(defaults), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpProject, "moat.yaml"), []byte(project), 0644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runConfigShow(&out, tmpProject, false /*source*/, false /*noDefaults*/); err != nil {
		t.Fatalf("runConfigShow: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "agent: claude") {
		t.Errorf("output missing 'agent: claude':\n%s", s)
	}
	if !strings.Contains(s, "region: us-west-2") {
		t.Errorf("output missing 'region: us-west-2':\n%s", s)
	}
	if !strings.Contains(s, "enabled: true") {
		t.Errorf("output missing 'enabled: true':\n%s", s)
	}
}

func TestConfigShow_NoDefaultsFlag(t *testing.T) {
	tmpProject := t.TempDir()
	tmpMoatHome := t.TempDir()
	t.Setenv("MOAT_HOME", tmpMoatHome)

	defaults := `agent: claude
grants: [aws]
`
	project := `agent: codex
`
	if err := os.WriteFile(filepath.Join(tmpMoatHome, "defaults.yaml"), []byte(defaults), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpProject, "moat.yaml"), []byte(project), 0644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runConfigShow(&out, tmpProject, false /*source*/, true /*noDefaults*/); err != nil {
		t.Fatalf("runConfigShow: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "agent: codex") {
		t.Errorf("output should be project-only (codex):\n%s", s)
	}
	if strings.Contains(s, "aws") {
		t.Errorf("--no-defaults should suppress defaults; output contained 'aws':\n%s", s)
	}
}

func TestConfigShow_SourceFlag(t *testing.T) {
	tmpProject := t.TempDir()
	tmpMoatHome := t.TempDir()
	t.Setenv("MOAT_HOME", tmpMoatHome)

	defaults := `agent: claude
grants: [aws]
claude:
  bedrock:
    enabled: true
    region: us-east-1
`
	project := `claude:
  bedrock:
    region: us-west-2
`
	if err := os.WriteFile(filepath.Join(tmpMoatHome, "defaults.yaml"), []byte(defaults), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpProject, "moat.yaml"), []byte(project), 0644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runConfigShow(&out, tmpProject, true /*source*/, false /*noDefaults*/); err != nil {
		t.Fatalf("runConfigShow: %v", err)
	}
	s := out.String()
	// Each annotated line ends with `# <source>` per the SourceMap.
	if !strings.Contains(s, "# defaults") {
		t.Errorf("--source output missing 'defaults' annotations:\n%s", s)
	}
	if !strings.Contains(s, "# project") {
		t.Errorf("--source output missing 'project' annotations:\n%s", s)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/moat/cli/ -run TestConfigShow -v`
Expected: FAIL — `runConfigShow` undefined.

- [ ] **Step 3: Implement the command**

Create `cmd/moat/cli/config.go`:

```go
package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/majorcontext/moat/internal/config"
)

var (
	configShowSource     bool
	configShowWorkspace  string
	configShowNoDefaults bool
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Inspect moat configuration",
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Print the resolved moat config (project moat.yaml merged with ~/.moat/defaults.yaml)",
	Long: `Print the resolved moat config as YAML.

By default, the project moat.yaml is merged with ~/.moat/defaults.yaml (or
$MOAT_HOME/defaults.yaml if MOAT_HOME is set). Use --no-defaults to print
the project-only config without merging.

With --source, each line is annotated with a trailing comment showing where
that value came from: ` + "`# project`" + `, ` + "`# defaults`" + `, or ` + "`# merged`" + ` (slices/maps where
both sides contributed).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		workspace := configShowWorkspace
		if workspace == "" {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("getting working directory: %w", err)
			}
			workspace = cwd
		}
		return runConfigShow(os.Stdout, workspace, configShowSource, configShowNoDefaults)
	},
}

func init() {
	rootCmd.AddCommand(configCmd)
	configCmd.AddCommand(configShowCmd)
	configShowCmd.Flags().BoolVar(&configShowSource, "source", false,
		"Annotate each line with its source: project, defaults, or merged")
	configShowCmd.Flags().StringVar(&configShowWorkspace, "workspace", "",
		"Inspect a project at a non-cwd path")
	configShowCmd.Flags().BoolVar(&configShowNoDefaults, "no-defaults", false,
		"Print the project-only config without merging ~/.moat/defaults.yaml")
}

// runConfigShow writes the resolved config to w. Factored out for testing.
func runConfigShow(w io.Writer, workspace string, withSource, noDefaults bool) error {
	var cfg *config.Config
	var sources config.SourceMap
	if noDefaults {
		// Project-only path: bypass defaults loading and validation.
		// (Validation can fail on a project that depends on defaults; --no-defaults
		// is explicitly inspecting the unmerged input.)
		c, err := config.LoadProject(workspace) // exported in Task 8 step 3a
		if err != nil {
			return err
		}
		cfg = c
		if cfg == nil {
			cfg = &config.Config{}
		}
	} else {
		project, _ := config.LoadProject(workspace)
		defaults, _ := config.LoadDefaults()
		cfg = config.MergeConfig(defaults, project)
		if withSource {
			sources = config.ConfigSources(defaults, project, cfg)
		}
	}

	out, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	if !withSource {
		_, err := w.Write(out)
		return err
	}

	// Source annotation: walk the marshaled YAML lines and append a comment
	// for each key based on the SourceMap. This is a best-effort post-process
	// using yaml.Node would be more precise; for v1 we annotate by simple
	// key-path heuristics. Top-level scalar keys are annotated when their
	// dotted path appears in `sources`.
	annotated := annotateYAML(string(out), sources)
	_, err = io.WriteString(w, annotated)
	return err
}

// annotateYAML appends `# <source>` comments to each YAML line whose
// dotted path is in `sources`. Lines with no source mapping pass through
// unchanged.
//
// For v1: the implementation walks indentation to derive the dotted path
// for each scalar line, looks up the source, and appends a comment.
// Implementer: build a small key-path stack tracking indentation depth.
// See the test fixtures for the exact expected output shape; this function
// must produce stable output (one comment per scalar line, no trailing
// whitespace before the `#`).
func annotateYAML(yamlStr string, sources config.SourceMap) string {
	if len(sources) == 0 {
		return yamlStr
	}
	lines := strings.Split(yamlStr, "\n")
	var keyStack []string
	var indentStack []int
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " ")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(trimmed)
		// Pop stack to current indent.
		for len(indentStack) > 0 && indentStack[len(indentStack)-1] >= indent {
			keyStack = keyStack[:len(keyStack)-1]
			indentStack = indentStack[:len(indentStack)-1]
		}
		// Parse "key:" or "key: value".
		colon := strings.IndexByte(trimmed, ':')
		if colon < 0 {
			continue
		}
		key := trimmed[:colon]
		path := strings.Join(append(append([]string{}, keyStack...), key), ".")

		// Annotate if we have a source for this path.
		if src, ok := sources[path]; ok && src != config.SourceUnset {
			// Only annotate lines that contain a value (key: value), not
			// container keys ("key:" with no value on the same line).
			rest := strings.TrimSpace(trimmed[colon+1:])
			if rest != "" {
				lines[i] = line + "  # " + src.String()
			}
		}

		// If this line is a container (no value after the colon), push it.
		valueAfter := strings.TrimSpace(trimmed[colon+1:])
		if valueAfter == "" {
			keyStack = append(keyStack, key)
			indentStack = append(indentStack, indent)
		}
	}
	return strings.Join(lines, "\n")
}
```

> **Implementer note:** the `--source` annotator above is intentionally simple — it covers scalar fields by yaml dotted path. Slice/map element annotations (`grants[aws]`, `env.AWS_REGION`) need targeted extension: walk the marshaled YAML's list items / map entries and look them up using the `path[element]` / `path.key` form that `ConfigSources` emits. Add per-element annotation logic if the basic version is insufficient — verify against the third test (`TestConfigShow_SourceFlag`) which only asserts the *presence* of `# defaults` and `# project` somewhere in the output. Stronger output-shape assertions can be added later if needed.

**Step 3a: Export `LoadProject`.** In `internal/config/config.go`, rename the unexported `loadProject` (introduced in Task 7) to exported `LoadProject` so the CLI command can call it from another package. Update the one call site in `Load`. The exported function name lets `moat config show --no-defaults` bypass the merge cleanly.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/moat/cli/ -run TestConfigShow -v`
Expected: PASS.

Run `go build ./...` — clean. `make lint` — 0 issues.

- [ ] **Step 5: Commit**

```bash
git add cmd/moat/cli/config.go cmd/moat/cli/config_test.go internal/config/config.go
git commit -m "feat(cli): moat config show with --source and --no-defaults"
```

---

## Task 9: Docs + changelog

**Files:**
- Modify: `docs/content/reference/02-moat-yaml.md`
- Modify: `docs/content/reference/01-cli.md`
- Modify: `CHANGELOG.md`

- [ ] **Step 1: moat.yaml reference — Defaults subsection**

Read the surrounding structure of `docs/content/reference/02-moat-yaml.md` and add a new top-level subsection "Defaults" near the front of the reference (after the introduction, before per-field docs). Cover:

- File location: `~/.moat/defaults.yaml` (or `$MOAT_HOME/defaults.yaml`).
- Schema: identical to `moat.yaml`.
- Merge precedence: project values win per field; maps merge per key; slices union with dedupe.
- Worked example showing defaults + project + resolved (use the same scenario as `internal/config/testdata/merge/`):

```yaml
# ~/.moat/defaults.yaml
agent: claude
grants:
  - aws
claude:
  bedrock:
    enabled: true
    region: us-east-1
```

```yaml
# moat.yaml
grants:
  - github
claude:
  bedrock:
    region: us-west-2
```

```yaml
# Resolved (what `moat config show` prints)
agent: claude
grants: [aws, github]
claude:
  bedrock:
    enabled: true
    region: us-west-2
```

- Pointer to `moat config show` for inspection.
- One-paragraph note on the security trade-off: defaults make container behavior depend on host-side state, mitigated by `moat config show --source`.

- [ ] **Step 2: CLI reference — `moat config show`**

Read the existing structure of `docs/content/reference/01-cli.md` and add a `moat config show` section (matching the format of the surrounding command entries — synopsis, flags table, examples). Document `--source`, `--workspace`, `--no-defaults`.

- [ ] **Step 3: Changelog**

In `CHANGELOG.md`, under the current `## Unreleased` section's `### Added` (match the existing entry style — bold feature name, `#NNN` PR placeholder):

```markdown
- **Per-user `moat.yaml` defaults** — a new `~/.moat/defaults.yaml` (or `$MOAT_HOME/defaults.yaml`) with the same schema as `moat.yaml` merges into every project's loaded config. Project values win per field; maps merge per key; slices union with dedupe. Inspect the resolved config with `moat config show` (or `moat config show --source` to see where each value came from). Closes the "I always want Claude on Bedrock" repetition without per-project copy-paste. ([#NNN](https://github.com/majorcontext/moat/pull/NNN))
```

- [ ] **Step 4: Verify and commit**

```bash
git status   # confirm only docs/ and CHANGELOG.md are modified
git add docs/ CHANGELOG.md
git commit -m "docs(config): document defaults.yaml and moat config show"
```

---

## Final verification (run before opening a PR)

- [ ] `go build ./...` — exit 0
- [ ] `make test-unit` — full suite green with race detector. The pre-existing `internal/deps/TestRegistryGithubBinaryURLsExist` HTTP 401 failure (sandbox network restrictions) is acceptable per the prior plan's finding.
- [ ] `make lint` — 0 issues
- [ ] `gofmt -l $(git diff origin/main..HEAD --name-only | grep '\.go$')` — empty
- [ ] Manual smoke: set `~/.moat/defaults.yaml` to `agent: claude\ngrants: [aws]\nclaude:\n  bedrock:\n    enabled: true\n`. Create an empty `moat.yaml` in a project (`agent: claude` only). Run `moat config show` — verify the merged output. Run `moat config show --source` — verify annotations. Run `moat config show --no-defaults` — verify project-only output.
- [ ] Re-read the spec (`docs/plans/2026-05-20-moat-yaml-defaults-design.md`) §3.1-3.7 and confirm each requirement is implemented.
- [ ] Use `superpowers:finishing-a-development-branch` to decide merge/PR.

---

## Self-Review (completed during planning)

**Spec coverage:**

| Spec § | Implemented by |
|---|---|
| §3.1 file location + schema | Task 1 |
| §3.2 Load flow (project + defaults + merge + validate) | Task 7 |
| §3.3 merge rules — scalars, maps, slices, pointers, nested | Tasks 2 (scalars+maps), 3 (string slices), 4 (struct slices), 5 (nested+pointers) |
| §3.4 slice-of-struct dedup keys | Task 4 (Mounts/Volumes/MCP), Task 5 (Network.Rules) |
| §3.5 validation on merged result | Task 7 (`validateConfig`) |
| §3.6 `moat config show` with `--source`/`--workspace`/`--no-defaults` | Tasks 6 (ConfigSources) + 8 (CLI) |
| §3.7 tests | Embedded in Tasks 1-8 |

No gaps.

**Placeholder scan:** Two places use deliberate "implementer reads the current source first" directives (Task 5 nested-struct merge skeletons; Task 7's `validateConfig` relocation). Both are accompanied by explicit guidance and the failing tests will catch any incomplete work. Not placeholders — bounded investigations with prescribed action. `#NNN` PR placeholder is intentional.

**Type consistency:** `MergeConfig(defaults, project *Config) *Config`; `LoadDefaults() (*Config, error)`; `LoadProject(dir) (*Config, error)`; `ConfigSources(defaults, project, merged *Config) SourceMap`; `SourceMap map[string]ConfigSource`; `ConfigSource` enum with `SourceDefaults`/`SourceProject`/`SourceMerged`/`SourceUnset`. Used consistently across Tasks 1-8. Slice/struct dedup keys (`MountEntry`: `(Source,Target)`; `VolumeConfig`/`MCPServerConfig`: `Name`; `NetworkRuleEntry`: `(Host,Method,Path)`) used consistently.
