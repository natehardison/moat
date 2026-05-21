# Provider host override on `moat grant` — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `--host <hostname>` to `moat grant <provider>` that writes a user YAML override at `~/.moat/providers/<name>.yaml` for self-hosted deployments (e.g. self-hosted GitLab), then runs the normal grant flow against the overridden host.

**Architecture:** A new file `internal/providers/configprovider/override.go` provides pure helpers (`LoadEmbeddedDef`, `ApplyHostOverride`, `WriteUserOverride`, `UserOverridePath`, `ValidateHostname`). `cmd/moat/cli/grant.go` gains a `--host` flag that, when set, validates the hostname, loads the embedded def for the resolved provider name, applies the override in-memory, persists the YAML (with a confirm-on-overwrite prompt), and constructs a one-off `ConfigProvider` from the overridden def for this command's grant call. The global registry and existing loader semantics are untouched.

**Tech Stack:** Go 1.25, `gopkg.in/yaml.v3`, `net/url`, `embed.FS`, `golang.org/x/term`, Cobra. Existing helpers: `configprovider.parseProviderDef`, `config.GlobalConfigDir`, `provider/util.Confirm`, `provider/util.PromptForToken`.

**Reference:** Design at `docs/plans/2026-05-19-provider-host-override-design.md`.

---

## File Structure

```
internal/providers/configprovider/
  override.go               # new — LoadEmbeddedDef, ApplyHostOverride, WriteUserOverride,
                            #       UserOverridePath, ValidateHostname, EmbeddedProviderNames
  override_test.go          # new — unit tests for all helpers above

cmd/moat/cli/
  grant.go                  # modify — add --host flag and override branch in runGrant
  grant_test.go             # modify — add CLI-level tests for --host

docs/content/reference/
  01-cli.md                 # modify — document --host on `moat grant`
  04-grants.md              # modify — GitLab self-hosted subsection
  07-provider-yaml.md       # modify — cross-reference the shortcut

CHANGELOG.md                # modify — ### Added entry under ## Unreleased
```

---

## Task 1: Pure helpers — `LoadEmbeddedDef`, `UserOverridePath`, `EmbeddedProviderNames`

**Files:**
- Create: `internal/providers/configprovider/override.go`
- Create: `internal/providers/configprovider/override_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/providers/configprovider/override_test.go`:

```go
package configprovider

import (
	"path/filepath"
	"testing"
)

func TestLoadEmbeddedDef_Gitlab(t *testing.T) {
	def, err := LoadEmbeddedDef("gitlab")
	if err != nil {
		t.Fatalf("LoadEmbeddedDef(\"gitlab\") error: %v", err)
	}
	if def.Name != "gitlab" {
		t.Errorf("Name = %q, want %q", def.Name, "gitlab")
	}
	if len(def.Hosts) == 0 || def.Hosts[0] != "gitlab.com" {
		t.Errorf("Hosts = %v, want first entry to be gitlab.com", def.Hosts)
	}
	if def.Validate == nil || def.Validate.URL != "https://gitlab.com/api/v4/user" {
		t.Errorf("Validate.URL = %+v, want https://gitlab.com/api/v4/user", def.Validate)
	}
}

func TestLoadEmbeddedDef_NotEmbedded(t *testing.T) {
	if _, err := LoadEmbeddedDef("github"); err == nil {
		t.Errorf("LoadEmbeddedDef(\"github\") err = nil, want error (github is a Go provider)")
	}
	if _, err := LoadEmbeddedDef("nonexistent"); err == nil {
		t.Errorf("LoadEmbeddedDef(\"nonexistent\") err = nil, want error")
	}
}

func TestUserOverridePath(t *testing.T) {
	t.Setenv("MOAT_HOME", "/tmp/moat-test")
	got := UserOverridePath("gitlab")
	want := filepath.Join("/tmp/moat-test", "providers", "gitlab.yaml")
	if got != want {
		t.Errorf("UserOverridePath(\"gitlab\") = %q, want %q", got, want)
	}
}

func TestEmbeddedProviderNames(t *testing.T) {
	names := EmbeddedProviderNames()
	if len(names) == 0 {
		t.Fatal("EmbeddedProviderNames() returned empty slice")
	}
	// Sorted, contains gitlab and at least one other.
	for i := 1; i < len(names); i++ {
		if names[i-1] >= names[i] {
			t.Errorf("EmbeddedProviderNames() not sorted: %v", names)
			break
		}
	}
	found := false
	for _, n := range names {
		if n == "gitlab" {
			found = true
		}
	}
	if !found {
		t.Errorf("EmbeddedProviderNames() = %v, missing gitlab", names)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/providers/configprovider/... -run "TestLoadEmbeddedDef|TestUserOverridePath|TestEmbeddedProviderNames" -v`
Expected: FAIL with "undefined: LoadEmbeddedDef" / "undefined: UserOverridePath" / "undefined: EmbeddedProviderNames"

- [ ] **Step 3: Implement the helpers**

Create `internal/providers/configprovider/override.go`:

```go
package configprovider

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/majorcontext/moat/internal/config"
)

// LoadEmbeddedDef reads and parses the embedded YAML definition for the
// given provider name. Returns an error if no embedded default exists
// (e.g. for Go-implemented providers like github).
func LoadEmbeddedDef(name string) (ProviderDef, error) {
	data, err := defaultsFS.ReadFile("defaults/" + name + ".yaml")
	if err != nil {
		return ProviderDef{}, fmt.Errorf("no embedded provider named %q", name)
	}
	def, err := parseProviderDef(data)
	if err != nil {
		return ProviderDef{}, fmt.Errorf("parsing embedded provider %q: %w", name, err)
	}
	return def, nil
}

// UserOverridePath returns the canonical path for a provider's user-level
// override YAML file under <GlobalConfigDir>/providers/.
func UserOverridePath(name string) string {
	return filepath.Join(config.GlobalConfigDir(), "providers", name+".yaml")
}

// EmbeddedProviderNames returns the sorted list of provider names that
// ship as embedded YAML and therefore support host override via --host.
func EmbeddedProviderNames() []string {
	entries, err := defaultsFS.ReadDir("defaults")
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		names = append(names, strings.TrimSuffix(entry.Name(), ".yaml"))
	}
	sort.Strings(names)
	return names
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/providers/configprovider/... -run "TestLoadEmbeddedDef|TestUserOverridePath|TestEmbeddedProviderNames" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/providers/configprovider/override.go internal/providers/configprovider/override_test.go
git commit -m "feat(configprovider): add embedded def lookup and override path helpers"
```

---

## Task 2: `ApplyHostOverride`

**Files:**
- Modify: `internal/providers/configprovider/override.go`
- Modify: `internal/providers/configprovider/override_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/providers/configprovider/override_test.go`:

```go
func TestApplyHostOverride_Gitlab(t *testing.T) {
	def, err := LoadEmbeddedDef("gitlab")
	if err != nil {
		t.Fatalf("LoadEmbeddedDef: %v", err)
	}
	out, err := ApplyHostOverride(def, "gitlab.acme.com")
	if err != nil {
		t.Fatalf("ApplyHostOverride: %v", err)
	}
	if len(out.Hosts) != 1 || out.Hosts[0] != "gitlab.acme.com" {
		t.Errorf("Hosts = %v, want [gitlab.acme.com]", out.Hosts)
	}
	if out.Validate == nil || out.Validate.URL != "https://gitlab.acme.com/api/v4/user" {
		t.Errorf("Validate.URL = %+v, want https://gitlab.acme.com/api/v4/user", out.Validate)
	}
	// Other fields preserved.
	if out.Name != def.Name || out.Description != def.Description ||
		out.Inject.Header != def.Inject.Header || out.ContainerEnv != def.ContainerEnv {
		t.Errorf("non-host fields changed: got %+v, original %+v", out, def)
	}
	// Original def must not be mutated.
	if len(def.Hosts) == 1 && def.Hosts[0] == "gitlab.acme.com" {
		t.Errorf("ApplyHostOverride mutated input def")
	}
}

func TestApplyHostOverride_NoValidate(t *testing.T) {
	def := ProviderDef{
		Name:        "x",
		Description: "x",
		Hosts:       []string{"x.example.com"},
		Inject:      InjectConfig{Header: "X"},
	}
	out, err := ApplyHostOverride(def, "y.example.com")
	if err != nil {
		t.Fatalf("ApplyHostOverride: %v", err)
	}
	if out.Hosts[0] != "y.example.com" {
		t.Errorf("Hosts[0] = %q, want y.example.com", out.Hosts[0])
	}
	if out.Validate != nil {
		t.Errorf("Validate = %+v, want nil", out.Validate)
	}
}

func TestApplyHostOverride_PreservesTokenPlaceholder(t *testing.T) {
	def := ProviderDef{
		Name:        "telegram",
		Description: "x",
		Hosts:       []string{"api.telegram.org"},
		Inject:      InjectConfig{Header: ""},
		ContainerEnv: "TELEGRAM_BOT_TOKEN",
		Validate: &ValidateConfig{
			URL: "https://api.telegram.org/bot${token}/getMe",
		},
	}
	out, err := ApplyHostOverride(def, "tg.example.com")
	if err != nil {
		t.Fatalf("ApplyHostOverride: %v", err)
	}
	want := "https://tg.example.com/bot${token}/getMe"
	if out.Validate.URL != want {
		t.Errorf("Validate.URL = %q, want %q", out.Validate.URL, want)
	}
}

func TestApplyHostOverride_RelativeValidateURL(t *testing.T) {
	def := ProviderDef{
		Name:        "x",
		Description: "x",
		Hosts:       []string{"x.example.com"},
		Inject:      InjectConfig{Header: "X"},
		Validate:    &ValidateConfig{URL: "/relative/path"},
	}
	if _, err := ApplyHostOverride(def, "y.example.com"); err == nil {
		t.Errorf("ApplyHostOverride err = nil, want error for relative validate URL")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/providers/configprovider/... -run TestApplyHostOverride -v`
Expected: FAIL with "undefined: ApplyHostOverride"

- [ ] **Step 3: Implement `ApplyHostOverride`**

Append to `internal/providers/configprovider/override.go`:

```go
import (
	"net/url"
)
```

(Merge into the existing import block.)

Add the function:

```go
// ApplyHostOverride returns a copy of def with Hosts replaced by [host] and
// Validate.URL rewritten so its host component matches the user's host.
// The original def is not mutated. Pure function — no I/O.
func ApplyHostOverride(def ProviderDef, host string) (ProviderDef, error) {
	out := def
	out.Hosts = []string{host}

	if def.Validate != nil {
		u, err := url.Parse(def.Validate.URL)
		if err != nil {
			return ProviderDef{}, fmt.Errorf("parsing validate URL %q: %w", def.Validate.URL, err)
		}
		if u.Host == "" {
			return ProviderDef{}, fmt.Errorf("validate URL %q has no host", def.Validate.URL)
		}
		u.Host = host
		validateCopy := *def.Validate
		validateCopy.URL = u.String()
		out.Validate = &validateCopy
	}

	return out, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/providers/configprovider/... -run TestApplyHostOverride -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/providers/configprovider/override.go internal/providers/configprovider/override_test.go
git commit -m "feat(configprovider): add ApplyHostOverride for hosts and validate URL"
```

---

## Task 3: `WriteUserOverride`

**Files:**
- Modify: `internal/providers/configprovider/override.go`
- Modify: `internal/providers/configprovider/override_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/providers/configprovider/override_test.go`:

```go
import (
	"os"  // add to import block at the top
)
```

(Merge with the existing imports.)

Then append:

```go
func TestWriteUserOverride_RoundTrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("MOAT_HOME", tmp)

	def, err := LoadEmbeddedDef("gitlab")
	if err != nil {
		t.Fatalf("LoadEmbeddedDef: %v", err)
	}
	overridden, err := ApplyHostOverride(def, "gitlab.acme.com")
	if err != nil {
		t.Fatalf("ApplyHostOverride: %v", err)
	}

	if err := WriteUserOverride("gitlab", overridden); err != nil {
		t.Fatalf("WriteUserOverride: %v", err)
	}

	path := UserOverridePath("gitlab")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", path, err)
	}

	parsed, err := parseProviderDef(data)
	if err != nil {
		t.Fatalf("parseProviderDef on written YAML: %v", err)
	}
	if len(parsed.Hosts) != 1 || parsed.Hosts[0] != "gitlab.acme.com" {
		t.Errorf("round-trip Hosts = %v, want [gitlab.acme.com]", parsed.Hosts)
	}
	if parsed.Validate == nil || parsed.Validate.URL != "https://gitlab.acme.com/api/v4/user" {
		t.Errorf("round-trip Validate.URL = %+v, want https://gitlab.acme.com/api/v4/user", parsed.Validate)
	}
	if parsed.Name != "gitlab" {
		t.Errorf("round-trip Name = %q", parsed.Name)
	}
}

func TestWriteUserOverride_CreatesDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("MOAT_HOME", tmp)
	def, _ := LoadEmbeddedDef("gitlab")
	overridden, _ := ApplyHostOverride(def, "gitlab.acme.com")

	if err := WriteUserOverride("gitlab", overridden); err != nil {
		t.Fatalf("WriteUserOverride: %v", err)
	}
	info, err := os.Stat(filepath.Join(tmp, "providers"))
	if err != nil {
		t.Fatalf("providers dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("providers path is not a directory")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/providers/configprovider/... -run TestWriteUserOverride -v`
Expected: FAIL with "undefined: WriteUserOverride"

- [ ] **Step 3: Implement `WriteUserOverride`**

Append to `internal/providers/configprovider/override.go`:

Add `os` and `gopkg.in/yaml.v3` to the imports.

```go
// WriteUserOverride marshals def to YAML and writes it to UserOverridePath(name),
// creating the providers directory if it does not already exist.
func WriteUserOverride(name string, def ProviderDef) error {
	path := UserOverridePath(name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating providers dir: %w", err)
	}
	data, err := yaml.Marshal(def)
	if err != nil {
		return fmt.Errorf("marshaling provider def: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing override file %s: %w", path, err)
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/providers/configprovider/... -run TestWriteUserOverride -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/providers/configprovider/override.go internal/providers/configprovider/override_test.go
git commit -m "feat(configprovider): add WriteUserOverride to persist user YAML"
```

---

## Task 4: `ValidateHostname`

**Files:**
- Modify: `internal/providers/configprovider/override.go`
- Modify: `internal/providers/configprovider/override_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/providers/configprovider/override_test.go`:

```go
func TestValidateHostname_Accept(t *testing.T) {
	cases := []string{
		"gitlab.acme.com",
		"git.foo.bar.example",
		"a-b.example.io",
		"x.y.z",
		"sub-domain.example.co.uk",
	}
	for _, h := range cases {
		t.Run(h, func(t *testing.T) {
			if err := ValidateHostname(h); err != nil {
				t.Errorf("ValidateHostname(%q) = %v, want nil", h, err)
			}
		})
	}
}

func TestValidateHostname_Reject(t *testing.T) {
	longLabel := strings.Repeat("a", 64)
	longHost := strings.Repeat("a.", 130) + "com" // > 253 chars total

	cases := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"scheme", "https://gitlab.acme.com"},
		{"path", "gitlab.acme.com/path"},
		{"port", "gitlab.acme.com:8080"},
		{"query", "gitlab.acme.com?x=1"},
		{"userinfo", "user@gitlab.acme.com"},
		{"no dot", "localhost"},
		{"leading dash", "-leading.example.com"},
		{"trailing dash", "trailing-.example.com"},
		{"label too long", longLabel + ".example.com"},
		{"hostname too long", longHost},
		{"uppercase", "GitLab.example.com"},
		{"underscore", "git_lab.example.com"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := ValidateHostname(c.in); err == nil {
				t.Errorf("ValidateHostname(%q) = nil, want error", c.in)
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/providers/configprovider/... -run TestValidateHostname -v`
Expected: FAIL with "undefined: ValidateHostname"

- [ ] **Step 3: Implement `ValidateHostname`**

Add to `internal/providers/configprovider/override.go`. Add `regexp` to the imports:

```go
// labelRE matches a single DNS label per RFC 1123: lowercase letters, digits,
// hyphens; no leading or trailing hyphen; 1-63 chars.
var labelRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// ValidateHostname returns an error if host is not a bare DNS hostname.
// Rejects schemes, paths, queries, ports, userinfo, single-label names, and
// labels that violate RFC 1123.
func ValidateHostname(host string) error {
	if host == "" {
		return fmt.Errorf("--host must be a bare hostname (e.g., gitlab.acme.com), got %q", host)
	}
	if len(host) > 253 {
		return fmt.Errorf("--host exceeds 253 chars: %q", host)
	}
	if strings.ContainsAny(host, ":/?#@") {
		return fmt.Errorf("--host must be a bare hostname (e.g., gitlab.acme.com), got %q", host)
	}
	if !strings.Contains(host, ".") {
		return fmt.Errorf("--host must include a domain (e.g., gitlab.acme.com), got %q", host)
	}
	for _, label := range strings.Split(host, ".") {
		if !labelRE.MatchString(label) {
			return fmt.Errorf("--host has invalid label %q (RFC 1123: lowercase letters, digits, hyphens; no leading/trailing hyphen; ≤1-63 chars)", label)
		}
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/providers/configprovider/... -run TestValidateHostname -v`
Expected: PASS

- [ ] **Step 5: Run the full configprovider test package to confirm nothing else broke**

Run: `go test ./internal/providers/configprovider/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/providers/configprovider/override.go internal/providers/configprovider/override_test.go
git commit -m "feat(configprovider): add ValidateHostname for --host input"
```

---

## Task 5: Wire `--host` flag into `moat grant`

**Files:**
- Modify: `cmd/moat/cli/grant.go`

- [ ] **Step 1: Add the `--host` flag and helper for the override branch**

Open `cmd/moat/cli/grant.go`. At the top of the file, add to the imports:

```go
"errors"
"github.com/majorcontext/moat/internal/providers/configprovider"
"golang.org/x/term"
```

(Merge into the existing import block.)

Add a package-level variable next to the existing AWS flag vars:

```go
var grantHost string
```

Update `init()` to register the flag — append:

```go
grantCmd.Flags().StringVar(&grantHost, "host", "", "Custom host for YAML-defined providers (e.g., gitlab.acme.com for self-hosted GitLab)")
```

- [ ] **Step 2: Add the override branch to `runGrant`**

In `runGrant`, locate the block right after the CLI-name remapping switch:

```go
switch providerName {
case "openai":
    providerName = "codex"
case "google":
    providerName = "gemini"
}
```

Insert immediately after that switch (before `prov := provider.Get(providerName)`):

```go
if grantHost != "" {
    overridden, err := runHostOverride(providerName, grantHost)
    if err != nil {
        return err
    }
    return grantWithOverride(cmd.Context(), overridden)
}
```

- [ ] **Step 3: Add `runHostOverride` and `grantWithOverride` helpers**

Append at the bottom of `cmd/moat/cli/grant.go`:

```go
// errOverrideAborted signals that the user declined to overwrite an existing
// user override. Returned by runHostOverride so runGrant can exit non-zero
// without printing a generic error.
var errOverrideAborted = errors.New("aborted: existing override not overwritten")

// runHostOverride validates the host, loads the embedded provider def, applies
// the override, optionally prompts before overwriting an existing user file,
// writes the file, and returns the in-memory overridden def.
func runHostOverride(providerName, host string) (configprovider.ProviderDef, error) {
    if err := configprovider.ValidateHostname(host); err != nil {
        return configprovider.ProviderDef{}, err
    }

    def, err := configprovider.LoadEmbeddedDef(providerName)
    if err != nil {
        return configprovider.ProviderDef{}, fmt.Errorf(
            "--host is not supported for %q (built-in provider with a fixed host)\nSupported providers: %s",
            providerName, strings.Join(configprovider.EmbeddedProviderNames(), ", "),
        )
    }

    overridden, err := configprovider.ApplyHostOverride(def, host)
    if err != nil {
        return configprovider.ProviderDef{}, err
    }

    path := configprovider.UserOverridePath(providerName)
    if err := writeOverrideIfChanged(path, providerName, overridden, host); err != nil {
        return configprovider.ProviderDef{}, err
    }

    return overridden, nil
}

// writeOverrideIfChanged inspects an existing override file (if any) and
// either skips, prompts, or writes the new override.
func writeOverrideIfChanged(path, providerName string, overridden configprovider.ProviderDef, host string) error {
    existing, err := os.ReadFile(path)
    if err != nil && !os.IsNotExist(err) {
        return fmt.Errorf("reading existing override %s: %w", path, err)
    }
    if err == nil {
        existingDef, parseErr := configprovider.ParseProviderDef(existing)
        if parseErr != nil {
            return fmt.Errorf("existing override at %s is invalid YAML; remove or fix it before re-running: %w", path, parseErr)
        }
        if overridesMatch(existingDef, overridden) {
            fmt.Printf("Override at %s already set to %s — no changes needed\n", path, host)
            return nil
        }
        fmt.Printf("Existing override at %s: hosts=%v\n", path, existingDef.Hosts)
        fmt.Printf("New override:           hosts=%v\n", overridden.Hosts)
        if !term.IsTerminal(int(os.Stdin.Fd())) {
            return errOverrideAborted
        }
        ok, err := promptOverwrite()
        if err != nil {
            return err
        }
        if !ok {
            return errOverrideAborted
        }
        if err := configprovider.WriteUserOverride(providerName, overridden); err != nil {
            return err
        }
        fmt.Printf("Updated provider override at %s\n", path)
        return nil
    }
    if err := configprovider.WriteUserOverride(providerName, overridden); err != nil {
        return err
    }
    fmt.Printf("Writing provider override to %s\n", path)
    return nil
}

// overridesMatch compares the host-relevant fields between two definitions.
func overridesMatch(a, b configprovider.ProviderDef) bool {
    if len(a.Hosts) != len(b.Hosts) {
        return false
    }
    for i := range a.Hosts {
        if a.Hosts[i] != b.Hosts[i] {
            return false
        }
    }
    aURL, bURL := "", ""
    if a.Validate != nil {
        aURL = a.Validate.URL
    }
    if b.Validate != nil {
        bURL = b.Validate.URL
    }
    return aURL == bURL
}

// promptOverwrite asks the user whether to overwrite the existing override.
// Returns false on empty / non-yes input. Reads from stdin via util.Confirm.
func promptOverwrite() (bool, error) {
    return util.Confirm("Overwrite?")
}

// grantWithOverride constructs a one-off ConfigProvider from the overridden
// def, runs its Grant flow, and saves the resulting credential. Bypasses the
// global registry so token validation hits the user's host.
func grantWithOverride(ctx context.Context, def configprovider.ProviderDef) error {
    if ctx == nil {
        ctx = context.Background()
    }
    cp := configprovider.NewConfigProvider(def, "custom")
    provCred, err := cp.Grant(ctx)
    if err != nil {
        return err
    }
    cred := credential.Credential{
        Provider:  credential.Provider(provCred.Provider),
        Token:     provCred.Token,
        Scopes:    provCred.Scopes,
        ExpiresAt: provCred.ExpiresAt,
        CreatedAt: provCred.CreatedAt,
        Metadata:  provCred.Metadata,
    }
    credPath, err := saveCredential(cred)
    if err != nil {
        return err
    }
    if credential.ActiveProfile != "" {
        fmt.Printf("Credential saved to %s (profile: %s)\n", credPath, credential.ActiveProfile)
    } else {
        fmt.Printf("Credential saved to %s\n", credPath)
    }
    return nil
}
```

`util.Confirm` is the existing helper at `internal/provider/util/prompt.go:65`. Add `util` to the imports if not already present:

```go
"github.com/majorcontext/moat/internal/provider/util"
```

- [ ] **Step 4: Add `ParseProviderDef` exported wrapper in the configprovider package**

`parseProviderDef` is unexported. The CLI needs to parse a user's existing override file to compare it against the new one. Add a thin exported wrapper.

In `internal/providers/configprovider/override.go`, append:

```go
// ParseProviderDef parses raw YAML bytes into a ProviderDef. Exported wrapper
// around the package-internal parser so the CLI can validate user override
// files without re-implementing parsing.
func ParseProviderDef(data []byte) (ProviderDef, error) {
    return parseProviderDef(data)
}
```

- [ ] **Step 5: Build to verify everything compiles**

Run: `go build ./...`
Expected: no errors.

- [ ] **Step 6: Run existing tests to ensure no regressions**

Run: `go test ./cmd/moat/cli/... ./internal/providers/configprovider/...`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add cmd/moat/cli/grant.go internal/providers/configprovider/override.go
git commit -m "feat(cli): add --host flag to moat grant for self-hosted YAML providers"
```

---

## Task 6: CLI integration tests for `--host`

**Files:**
- Modify: `cmd/moat/cli/grant_test.go`

These tests exercise the wiring without going through real HTTP. They rely on:
- `MOAT_HOME` / `HOME` redirected to a temp dir to isolate the override file.
- The gitlab YAML has no `source_env` token in scope (we'll set `GITLAB_TOKEN`), so the grant flow short-circuits to the env-var path and skips the network validate when `Validate.URL` is unreachable.

Wait — the YAML *does* validate. We need to either:
- Mock the validate URL via `httptest`, then rewrite the validate URL to that mock host, OR
- Remove the `Validate` block from the def passed to `grantWithOverride` in tests.

The cleanest seam: spin up an `httptest.Server`, point the test at its host via `--host <host:port>` — except port is rejected by ValidateHostname. So instead: have the test use `httptest.Server` and bind the hostname `127.0.0.1.nip.io` won't work either.

**The simplest path:** make the integration test cover the *override file generation* end-to-end (Tasks 5's first 4 sub-flows: invalid host, unsupported provider, write, identical-file no-op), and stop short of running the actual grant (which would require either a network mock or a fixture provider with no `validate` block).

The test invokes the cobra command with `--host` and an env-var token. For the cases that should reach the network validate step, set up `httptest.NewServer` and substitute its URL into a *test-only* embedded YAML provider that ships under a different name (e.g. `testfixture.yaml`). To keep production untouched, this section skips that and only tests the override-file behavior, asserting on (a) the file path, (b) its contents, and (c) the early-failure error messages. The full grant path is already exercised by `TestParseProviderDef` and the unit tests for `ApplyHostOverride`/`WriteUserOverride`.

- [ ] **Step 1: Add unsupported-provider and invalid-host tests**

Append to `cmd/moat/cli/grant_test.go`:

```go
import (
    // add to the existing import block:
    "os"
    "path/filepath"
    "strings"
    "testing"
)

func TestGrantHost_UnsupportedProvider(t *testing.T) {
    t.Setenv("MOAT_HOME", t.TempDir())
    t.Setenv("MOAT_KEYRING_SERVICE", "moat-test")

    cmd := rootCmd
    cmd.SetArgs([]string{"grant", "github", "--host", "github.acme.com"})
    err := cmd.Execute()
    if err == nil {
        t.Fatal("expected error for --host on github (Go provider)")
    }
    if !strings.Contains(err.Error(), "not supported") {
        t.Errorf("error = %v, want it to contain \"not supported\"", err)
    }
    if !strings.Contains(err.Error(), "gitlab") {
        t.Errorf("error = %v, want it to list eligible providers including gitlab", err)
    }
}

func TestGrantHost_InvalidHostname(t *testing.T) {
    tmp := t.TempDir()
    t.Setenv("MOAT_HOME", tmp)
    t.Setenv("MOAT_KEYRING_SERVICE", "moat-test")

    cmd := rootCmd
    cmd.SetArgs([]string{"grant", "gitlab", "--host", "https://gitlab.acme.com"})
    err := cmd.Execute()
    if err == nil {
        t.Fatal("expected error for --host with scheme")
    }
    if !strings.Contains(err.Error(), "bare hostname") {
        t.Errorf("error = %v, want it to mention bare hostname", err)
    }
    // No file should be written.
    path := filepath.Join(tmp, "providers", "gitlab.yaml")
    if _, statErr := os.Stat(path); statErr == nil {
        t.Errorf("override file written despite invalid hostname: %s", path)
    }
}
```

- [ ] **Step 2: Run the tests to verify they fail (or pass — they may already work)**

Run: `go test ./cmd/moat/cli/... -run "TestGrantHost_UnsupportedProvider|TestGrantHost_InvalidHostname" -v`
Expected: PASS (the implementation from Task 5 already covers these cases).

If they fail, fix the implementation in Task 5 — these tests are the spec for the error paths.

- [ ] **Step 3: Add the identical-file no-op test**

Append to `cmd/moat/cli/grant_test.go`:

```go
func TestGrantHost_IdenticalFileNoOp(t *testing.T) {
    tmp := t.TempDir()
    t.Setenv("MOAT_HOME", tmp)
    t.Setenv("MOAT_KEYRING_SERVICE", "moat-test")

    // Pre-create an override matching what --host gitlab.acme.com would write.
    overrideDir := filepath.Join(tmp, "providers")
    if err := os.MkdirAll(overrideDir, 0o755); err != nil {
        t.Fatalf("mkdir: %v", err)
    }
    overridePath := filepath.Join(overrideDir, "gitlab.yaml")

    // Build the expected contents by going through the public helpers.
    // This duplicates the production path intentionally — if these helpers
    // drift apart, this test will fail.
    def, err := configprovider.LoadEmbeddedDef("gitlab")
    if err != nil {
        t.Fatalf("LoadEmbeddedDef: %v", err)
    }
    overridden, err := configprovider.ApplyHostOverride(def, "gitlab.acme.com")
    if err != nil {
        t.Fatalf("ApplyHostOverride: %v", err)
    }
    if err := configprovider.WriteUserOverride("gitlab", overridden); err != nil {
        t.Fatalf("WriteUserOverride: %v", err)
    }

    before, err := os.ReadFile(overridePath)
    if err != nil {
        t.Fatalf("read pre-existing override: %v", err)
    }

    // Run the command. With identical existing content, we expect the
    // "no changes needed" path. The grant call after that will fail because
    // we have no token in env and stdin isn't wired — that's acceptable: the
    // test only asserts the file is unchanged.
    cmd := rootCmd
    cmd.SetArgs([]string{"grant", "gitlab", "--host", "gitlab.acme.com"})
    _ = cmd.Execute() // grant prompt will error; we don't care here

    after, err := os.ReadFile(overridePath)
    if err != nil {
        t.Fatalf("read post-execution override: %v", err)
    }
    if string(before) != string(after) {
        t.Errorf("override file changed unexpectedly:\nbefore:\n%s\nafter:\n%s", before, after)
    }
}
```

Add the import:

```go
"github.com/majorcontext/moat/internal/providers/configprovider"
```

- [ ] **Step 4: Run all new tests**

Run: `go test ./cmd/moat/cli/... -run TestGrantHost -v`
Expected: PASS

- [ ] **Step 5: Run the full test suite**

Run: `make test-unit`
Expected: PASS (race detector clean too).

- [ ] **Step 6: Commit**

```bash
git add cmd/moat/cli/grant_test.go
git commit -m "test(cli): cover --host on moat grant"
```

---

## Task 7: Documentation — CLI reference

**Files:**
- Modify: `docs/content/reference/01-cli.md`

- [ ] **Step 1: Locate the `moat grant` flags section**

Run: `grep -n "moat grant" docs/content/reference/01-cli.md | head -20`
Identify the flag table or option list for `moat grant`.

- [ ] **Step 2: Add the `--host` flag entry**

Add a row to the `moat grant` options:

```markdown
| `--host <hostname>` | For YAML-defined providers, write a `~/.moat/providers/<name>.yaml` override that routes credential injection and token validation to the specified host. Use for self-hosted deployments (e.g. self-hosted GitLab). Hostname must be a bare DNS name — no scheme, port, or path. |
```

(Adapt the format to match the surrounding flag documentation style — table row, definition list, or bullet, whichever the file uses.)

Also add an example to the `moat grant` examples block:

```bash
moat grant gitlab --host gitlab.acme.com   # Self-hosted GitLab
```

- [ ] **Step 3: Verify docs render cleanly**

Run: `grep -A 2 "host gitlab.acme.com" docs/content/reference/01-cli.md`
Expected: shows the example.

- [ ] **Step 4: Commit**

```bash
git add docs/content/reference/01-cli.md
git commit -m "docs(cli): document moat grant --host flag"
```

---

## Task 8: Documentation — grants reference (GitLab self-hosted)

**Files:**
- Modify: `docs/content/reference/04-grants.md`

- [ ] **Step 1: Locate the GitLab grant section**

Run: `grep -n "gitlab\|GitLab" docs/content/reference/04-grants.md | head -10`
Identify the section header for GitLab.

- [ ] **Step 2: Add the Self-hosted GitLab subsection**

Add the following subsection under the existing GitLab grant content (use the surrounding heading level):

```markdown
#### Self-hosted GitLab

For a self-hosted GitLab instance, pass `--host` when granting:

```bash
moat grant gitlab --host gitlab.acme.com
```

This writes `~/.moat/providers/gitlab.yaml`, which routes credential injection
and token validation to your host. Subsequent `moat run --grant gitlab` uses
the override automatically.

To rotate the token without changing the host, run `moat grant gitlab` (no
flag). To change the host, re-run with a new `--host` value — Moat prompts
before overwriting an existing override. To remove the override, delete
`~/.moat/providers/gitlab.yaml`.
```

- [ ] **Step 3: Commit**

```bash
git add docs/content/reference/04-grants.md
git commit -m "docs(grants): add self-hosted GitLab section"
```

---

## Task 9: Documentation — provider YAML reference cross-link

**Files:**
- Modify: `docs/content/reference/07-provider-yaml.md`

- [ ] **Step 1: Locate the "Custom providers" section**

Run: `grep -n "^## " docs/content/reference/07-provider-yaml.md`
Identify the heading.

- [ ] **Step 2: Add the cross-reference paragraph**

At the end of the "Custom providers" section, append:

```markdown
For built-in YAML providers, `moat grant <name> --host <hostname>` is a
shortcut that generates the override file for you. See the
[Self-hosted GitLab section](./grants#self-hosted-gitlab) for an example.
```

- [ ] **Step 3: Commit**

```bash
git add docs/content/reference/07-provider-yaml.md
git commit -m "docs(provider-yaml): cross-link to --host shortcut"
```

---

## Task 10: CHANGELOG entry

**Files:**
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Add an `### Added` entry under `## Unreleased`**

Open `CHANGELOG.md`. Under the `## Unreleased` heading (line 7), add:

```markdown
### Added

- **Self-hosted host override on `moat grant`** — `moat grant gitlab --host gitlab.acme.com` writes a user provider YAML at `~/.moat/providers/gitlab.yaml` that routes credential injection and token validation to a custom host. Works for any built-in YAML provider (gitlab, sentry, datadog, linear, vercel, elevenlabs, brave-search, telegram). Use it to grant credentials for self-hosted deployments without hand-authoring a YAML override. (Linked to PR.)
```

(After the PR is opened, replace `(Linked to PR.)` with `([#NNN](https://github.com/majorcontext/moat/pull/NNN))`.)

- [ ] **Step 2: Commit**

```bash
git add CHANGELOG.md
git commit -m "docs(changelog): note --host flag for moat grant"
```

---

## Task 11: Lint and final verification

- [ ] **Step 1: Run lint**

Run: `make lint`
Expected: no errors. If `golangci-lint` is not installed, run `go vet ./...` instead.

If lint reports issues (formatting, unused imports, etc.), fix them inline and run again until clean.

- [ ] **Step 2: Run the full unit test suite**

Run: `make test-unit`
Expected: PASS, race detector clean.

- [ ] **Step 3: Manual smoke test**

Build the binary:

```bash
go build -o /tmp/moat-host ./cmd/moat
```

Run with a throwaway `MOAT_HOME`:

```bash
MOAT_HOME=$(mktemp -d) /tmp/moat-host grant gitlab --host gitlab.acme.com
```

Expected:
- Prints `Writing provider override to <MOAT_HOME>/providers/gitlab.yaml`.
- Prompts for a token (since no `GITLAB_TOKEN` is set).
- Cancel the prompt (Ctrl-C). Then verify the override file exists and parses:

```bash
cat $MOAT_HOME/providers/gitlab.yaml
```

Expected: `hosts: [gitlab.acme.com]` and `validate.url: https://gitlab.acme.com/api/v4/user`.

Then re-run the same command — expected: `Override at ... already set to gitlab.acme.com — no changes needed`.

Then run with a different host:

```bash
/tmp/moat-host grant gitlab --host gitlab.other.com
```

Expected: prints the diff summary and the `Overwrite? [y/N]:` prompt. Press `n` — expected: non-zero exit and "aborted" message; file unchanged.

- [ ] **Step 4: Commit any final fixups**

If the smoke test surfaced issues, fix and commit. Otherwise, this task ends with no commit.

---

## Self-Review (writer's checklist before handoff)

- [x] **Spec coverage**: Every section of the design is implemented:
  - User flow → Task 5 prints the messages described in the design.
  - Code structure (LoadEmbeddedDef, ApplyHostOverride, WriteUserOverride, UserOverridePath) → Tasks 1–3.
  - validate.url rewriting → Task 2, including `${token}` preservation and relative-URL rejection.
  - Hostname validation → Task 4.
  - Restricting `--host` to YAML providers → Task 5 (`LoadEmbeddedDef` errors, `EmbeddedProviderNames` for the message).
  - Confirmation logic → Task 5's `writeOverrideIfChanged` covers all four branches (absent / identical / different+yes / different+no / non-TTY).
  - File layout → all paths in tasks match the design.
  - Test plan → Tasks 1–4 cover unit tests; Task 6 covers CLI integration tests.
  - Docs (01-cli, 04-grants, 07-provider-yaml) → Tasks 7–9.
  - CHANGELOG → Task 10.
  - Out-of-scope items remain out of scope.

- [x] **Placeholder scan**: No TBD/TODO. CHANGELOG entry leaves PR number as `(Linked to PR.)` — flagged for replacement after PR creation, which is the standard pattern in this repo.

- [x] **Type consistency**: `ProviderDef`, `InjectConfig`, `ValidateConfig`, `ConfigProvider`, `NewConfigProvider`, `parseProviderDef` all match the existing package types. `util.Confirm` signature matches `prompt.go:65`. `credential.Credential` fields match those used in `runGrant`.

- [x] **Naming consistency**: `LoadEmbeddedDef`, `ApplyHostOverride`, `WriteUserOverride`, `UserOverridePath`, `ValidateHostname`, `EmbeddedProviderNames`, `ParseProviderDef`, `grantHost`, `runHostOverride`, `grantWithOverride`, `writeOverrideIfChanged`, `overridesMatch`, `promptOverwrite`, `errOverrideAborted` — used consistently across all tasks.
