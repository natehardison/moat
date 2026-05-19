# Claude-on-AWS-Bedrock Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let `agent: claude` run against AWS Bedrock by routing credentials through Claude Code's documented `awsCredentialExport` settings hook, reusing moat's existing AWS STS endpoint.

**Architecture:** Additive. A new `claude.bedrock` block in `moat.yaml` turns on Bedrock; a new `claude.env` map is merged into the container's `~/.claude/settings.json` `env` block (honoring the host settings.json `env`, including `AWS_PROFILE`/`AWS_REGION`). moat injects Bedrock model-ID env vars and writes `awsCredentialExport` pointing at the existing AWS credential helper, which gets a `--claude` mode; the AWS credential endpoint gains a `?format=claude` response shape. No proxy/gatekeeper changes.

**Tech Stack:** Go 1.x, standard library `encoding/json`, `net/http`, `net/http/httptest`; the repo's existing table-driven test style.

**Spec:** `docs/plans/2026-05-19-claude-bedrock-design.md` (read it before starting).

**Conventions (from CLAUDE.md):** Conventional Commits, NO `Co-Authored-By`. Run `go build ./...` and the touched-package tests before each commit. Use `make test-unit ARGS='-run TestName'` for single tests (adds the race detector).

---

## File Structure

| File | Responsibility | Action |
|---|---|---|
| `internal/config/config.go` | `ClaudeConfig.Env`, `ClaudeConfig.Bedrock`, `BedrockConfig`, `BedrockModels`, defaults, validation | Modify |
| `internal/config/config_test.go` | parse + validation tests | Modify |
| `internal/providers/claude/bedrock.go` | default model IDs, `BedrockEnv`, `BedrockTTLMillis` | Create |
| `internal/providers/claude/bedrock_test.go` | unit tests for the above | Create |
| `internal/providers/claude/settings.go` | `Settings.Env` first-class field + env merge with source tracking; `ConfigToSettings` maps `claude.env`; Bedrock-mode `awsCredentialExport`/`awsAuthRefresh` handling | Modify |
| `internal/providers/claude/settings_test.go` | env merge precedence + Bedrock-mode key handling tests | Modify |
| `internal/providers/aws/endpoint.go` | `?format=claude` response envelope | Modify |
| `internal/providers/aws/endpoint_test.go` | handler test for the envelope | Modify (or create if absent) |
| `internal/providers/aws/credential_helper.go` | `--claude` arg appends `?format=claude` | Modify |
| `internal/run/manager.go` | hoist settings load; region resolver; `AWS_PROFILE`→`[profile]`; inject Bedrock env + TTL; widen settings.json write gate; suppress `ANTHROPIC_*` | Modify |
| `docs/content/reference/02-moat-yaml.md` | document `claude.bedrock` + `claude.env` | Modify |
| `docs/content/guides/` | new Bedrock guide w/ agentbox hygiene `claude.env` example | Create |
| `CHANGELOG.md` | Added entry | Modify |

---

## Task 1: Config schema — `claude.env` and `claude.bedrock`

**Files:**
- Modify: `internal/config/config.go` (`ClaudeConfig` at lines 252-284; add new structs after it)
- Test: `internal/config/config_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/config/config_test.go`:

```go
func TestLoadClaudeBedrockConfig(t *testing.T) {
	dir := t.TempDir()
	yaml := `
agent: claude
grants:
  - aws
claude:
  env:
    CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: "1"
  bedrock:
    enabled: true
    region: us-east-1
    models:
      opus: custom.opus.id
`
	if err := os.WriteFile(filepath.Join(dir, "moat.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Claude.Env["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] != "1" {
		t.Errorf("claude.env not parsed: %#v", cfg.Claude.Env)
	}
	if cfg.Claude.Bedrock == nil || !cfg.Claude.Bedrock.Enabled {
		t.Fatalf("bedrock not parsed: %#v", cfg.Claude.Bedrock)
	}
	if cfg.Claude.Bedrock.Region != "us-east-1" {
		t.Errorf("region = %q, want us-east-1", cfg.Claude.Bedrock.Region)
	}
	if cfg.Claude.Bedrock.Models.Opus != "custom.opus.id" {
		t.Errorf("models.opus = %q", cfg.Claude.Bedrock.Models.Opus)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestLoadClaudeBedrockConfig -v`
Expected: FAIL — `cfg.Claude.Env` / `cfg.Claude.Bedrock` undefined (compile error).

- [ ] **Step 3: Add the fields and structs**

In `internal/config/config.go`, inside `type ClaudeConfig struct { ... }` add (after the `LLMGateway` field, before `SkipPermissionsPrompt`):

```go
	// Env is merged into the container's ~/.claude/settings.json "env" block.
	// Generic passthrough mirroring Claude Code's native settings.json env.
	// Use it for corp hygiene vars (telemetry/autoupdater off), AWS_REGION, etc.
	Env map[string]string `yaml:"env,omitempty"`

	// Bedrock routes Claude Code through AWS Bedrock instead of the Anthropic
	// API. Requires the "aws" grant. nil = disabled.
	Bedrock *BedrockConfig `yaml:"bedrock,omitempty"`
```

Immediately after the `ClaudeConfig` struct's closing brace, add:

```go
// BedrockConfig configures Claude Code → AWS Bedrock routing.
type BedrockConfig struct {
	Enabled bool          `yaml:"enabled,omitempty"`
	Region  string        `yaml:"region,omitempty"` // optional; overrides AWS grant region
	Models  BedrockModels `yaml:"models,omitempty"`
}

// BedrockModels overrides individual Bedrock model IDs. Empty fields fall
// back to built-in defaults (see internal/providers/claude/bedrock.go).
type BedrockModels struct {
	Haiku  string `yaml:"haiku,omitempty"`
	Sonnet string `yaml:"sonnet,omitempty"`
	Opus   string `yaml:"opus,omitempty"`
	Custom string `yaml:"custom,omitempty"`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -run TestLoadClaudeBedrockConfig -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): add claude.env and claude.bedrock config"
```

---

## Task 2: Config validation — Bedrock requires `aws`, excludes base_url/llm-gateway

**Files:**
- Modify: `internal/config/config.go` (`Load`, in the validation block near line 615, right after the existing `base_url && llm-gateway` check)
- Test: `internal/config/config_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestBedrockValidation(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "missing aws grant",
			yaml: "agent: claude\nclaude:\n  bedrock:\n    enabled: true\n",
			wantErr: "claude.bedrock requires the \"aws\" grant",
		},
		{
			name: "conflicts with base_url",
			yaml: "agent: claude\ngrants: [aws]\nclaude:\n  base_url: https://x.test\n  bedrock:\n    enabled: true\n",
			wantErr: "claude.bedrock is mutually exclusive with base_url",
		},
		{
			name: "conflicts with llm-gateway",
			yaml: "agent: claude\ngrants: [aws]\nclaude:\n  llm-gateway: {}\n  bedrock:\n    enabled: true\n",
			wantErr: "claude.bedrock is mutually exclusive with llm-gateway",
		},
		{
			name: "valid",
			yaml: "agent: claude\ngrants: [aws]\nclaude:\n  bedrock:\n    enabled: true\n",
			wantErr: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "moat.yaml"), []byte(tc.yaml), 0644); err != nil {
				t.Fatal(err)
			}
			_, err := Load(dir)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestBedrockValidation -v`
Expected: FAIL — no validation yet (the "missing aws grant" / conflict cases return nil error).

- [ ] **Step 3: Add validation**

In `internal/config/config.go`, in `Load`, immediately after the existing block:

```go
	if cfg.Claude.BaseURL != "" && cfg.Claude.LLMGateway != nil {
		return nil, fmt.Errorf("claude: base_url and llm-gateway are mutually exclusive — base_url routes to an external LLM proxy, llm-gateway routes to a local Keep sidecar")
	}
```

insert:

```go
	if cfg.Claude.Bedrock != nil && cfg.Claude.Bedrock.Enabled {
		hasAWS := false
		for _, g := range cfg.Grants {
			if g == "aws" || strings.HasPrefix(g, "aws:") {
				hasAWS = true
				break
			}
		}
		if !hasAWS {
			return nil, fmt.Errorf("claude.bedrock requires the \"aws\" grant — add 'aws' to grants and run 'moat grant aws <role-arn>'")
		}
		if cfg.Claude.BaseURL != "" {
			return nil, fmt.Errorf("claude.bedrock is mutually exclusive with base_url — Bedrock authenticates via AWS, base_url routes to an Anthropic-API proxy")
		}
		if cfg.Claude.LLMGateway != nil {
			return nil, fmt.Errorf("claude.bedrock is mutually exclusive with llm-gateway — Bedrock authenticates via AWS, llm-gateway routes to a local Keep sidecar")
		}
	}
```

(`strings` is already imported in config.go.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -run TestBedrockValidation -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): validate claude.bedrock grant + mutual exclusivity"
```

---

## Task 3: Bedrock env helper + default model IDs

**Files:**
- Create: `internal/providers/claude/bedrock.go`
- Test: `internal/providers/claude/bedrock_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/providers/claude/bedrock_test.go`:

```go
package claude

import (
	"testing"

	"github.com/majorcontext/moat/internal/config"
)

func TestBedrockEnvDefaults(t *testing.T) {
	env := BedrockEnv(config.BedrockConfig{Enabled: true})
	m := envSliceToMap(env)
	if m["CLAUDE_CODE_USE_BEDROCK"] != "1" {
		t.Errorf("CLAUDE_CODE_USE_BEDROCK = %q, want 1", m["CLAUDE_CODE_USE_BEDROCK"])
	}
	if m["ANTHROPIC_DEFAULT_SONNET_MODEL"] != defaultBedrockModels.Sonnet {
		t.Errorf("sonnet = %q, want default %q", m["ANTHROPIC_DEFAULT_SONNET_MODEL"], defaultBedrockModels.Sonnet)
	}
	if m["AWS_SDK_UA_APP_ID"] != "ClaudeCode-Sandbox" {
		t.Errorf("AWS_SDK_UA_APP_ID = %q", m["AWS_SDK_UA_APP_ID"])
	}
	if _, ok := m["AWS_SDK_LOAD_CONFIG"]; ok {
		t.Error("AWS_SDK_LOAD_CONFIG must NOT be set (Go-SDK-only flag)")
	}
}

func TestBedrockEnvOverride(t *testing.T) {
	env := BedrockEnv(config.BedrockConfig{
		Enabled: true,
		Models:  config.BedrockModels{Opus: "my.opus", Haiku: ""},
	})
	m := envSliceToMap(env)
	if m["ANTHROPIC_DEFAULT_OPUS_MODEL"] != "my.opus" {
		t.Errorf("opus override = %q, want my.opus", m["ANTHROPIC_DEFAULT_OPUS_MODEL"])
	}
	if m["ANTHROPIC_DEFAULT_HAIKU_MODEL"] != defaultBedrockModels.Haiku {
		t.Errorf("empty haiku should fall back to default, got %q", m["ANTHROPIC_DEFAULT_HAIKU_MODEL"])
	}
}

func TestBedrockTTLMillis(t *testing.T) {
	if got := BedrockTTLMillis(); got != "300000" {
		t.Errorf("BedrockTTLMillis() = %q, want 300000", got)
	}
}

func envSliceToMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, e := range env {
		for i := 0; i < len(e); i++ {
			if e[i] == '=' {
				m[e[:i]] = e[i+1:]
				break
			}
		}
	}
	return m
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/providers/claude/ -run TestBedrock -v`
Expected: FAIL — `BedrockEnv`, `defaultBedrockModels`, `BedrockTTLMillis` undefined.

- [ ] **Step 3: Create the implementation**

Create `internal/providers/claude/bedrock.go`:

```go
package claude

import "github.com/majorcontext/moat/internal/config"

// bedrockModelSet pairs a Bedrock model ID with its human display name.
type bedrockModelSet struct {
	Haiku, HaikuName   string
	Sonnet, SonnetName string
	Opus, OpusName     string
	Custom, CustomName string
}

// defaultBedrockModels mirrors the model IDs agentbox currently ships.
// Override individual entries via moat.yaml claude.bedrock.models.
var defaultBedrockModels = bedrockModelSet{
	Haiku: "global.anthropic.claude-haiku-4-5-20251001-v1:0", HaikuName: "Haiku 4.5",
	Sonnet: "global.anthropic.claude-sonnet-4-6[1m]", SonnetName: "Sonnet 4.6",
	Opus: "global.anthropic.claude-opus-4-6-v1[1m]", OpusName: "Opus 4.6",
	Custom: "global.anthropic.claude-opus-4-7[1m]", CustomName: "Opus 4.7",
}

// pick returns override if non-empty, else fallback.
func pick(override, fallback string) string {
	if override != "" {
		return override
	}
	return fallback
}

// BedrockEnv returns the moat-injected process env vars that put Claude Code
// into Bedrock mode. These are the highest-precedence env layer (spec §3.4):
// they are emitted via proxyEnv, not the merged settings.json env block.
//
// AWS_SDK_LOAD_CONFIG is deliberately NOT set: it is an AWS SDK for Go v1
// flag with no effect on Claude Code's bundled Rust AWS SDK.
func BedrockEnv(bc config.BedrockConfig) []string {
	haiku := pick(bc.Models.Haiku, defaultBedrockModels.Haiku)
	sonnet := pick(bc.Models.Sonnet, defaultBedrockModels.Sonnet)
	opus := pick(bc.Models.Opus, defaultBedrockModels.Opus)
	custom := pick(bc.Models.Custom, defaultBedrockModels.Custom)
	return []string{
		"CLAUDE_CODE_USE_BEDROCK=1",
		"AWS_SDK_UA_APP_ID=ClaudeCode-Sandbox",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL=" + haiku,
		"ANTHROPIC_DEFAULT_HAIKU_MODEL_NAME=" + defaultBedrockModels.HaikuName,
		"ANTHROPIC_DEFAULT_SONNET_MODEL=" + sonnet,
		"ANTHROPIC_DEFAULT_SONNET_MODEL_NAME=" + defaultBedrockModels.SonnetName,
		"ANTHROPIC_DEFAULT_OPUS_MODEL=" + opus,
		"ANTHROPIC_DEFAULT_OPUS_MODEL_NAME=" + defaultBedrockModels.OpusName,
		"ANTHROPIC_CUSTOM_MODEL_OPTION=" + custom,
		"ANTHROPIC_CUSTOM_MODEL_OPTION_NAME=" + defaultBedrockModels.CustomName,
	}
}

// BedrockTTLMillis is the value for CLAUDE_CODE_API_KEY_HELPER_TTL_MS, which
// controls how often Claude Code re-runs awsCredentialExport. The moat AWS
// endpoint already refreshes STS creds server-side and caches with a 5-minute
// pre-expiry buffer, so a conservative fixed 5-minute TTL is always safe.
func BedrockTTLMillis() string {
	return "300000"
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/providers/claude/ -run TestBedrock -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/providers/claude/bedrock.go internal/providers/claude/bedrock_test.go
git commit -m "feat(claude): add BedrockEnv helper and default model IDs"
```

---

## Task 4: `Settings.Env` as a first-class merged field

**Files:**
- Modify: `internal/providers/claude/settings.go` (`Settings` struct ~35-59; `knownSettingsKeys` ~84-88; `MarshalJSON` ~119-141; `MergeSettings` ~287-387; `ConfigToSettings` ~459-501)
- Test: `internal/providers/claude/settings_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/providers/claude/settings_test.go`:

```go
func TestEnvMergePrecedence(t *testing.T) {
	host := &Settings{Env: map[string]string{"AWS_REGION": "us-west-2", "HOST_ONLY": "h"}}
	project := &Settings{Env: map[string]string{"AWS_REGION": "eu-west-1", "PROJ": "p"}}
	yaml := &Settings{Env: map[string]string{"AWS_REGION": "us-east-1"}}

	r := MergeSettings(nil, host, SourceClaudeUser)
	r = MergeSettings(r, project, SourceProject)
	r = MergeSettings(r, yaml, SourceMoatYAML)

	if r.Env["AWS_REGION"] != "us-east-1" {
		t.Errorf("AWS_REGION = %q, want us-east-1 (moat.yaml wins)", r.Env["AWS_REGION"])
	}
	if r.Env["HOST_ONLY"] != "h" {
		t.Errorf("host-only env dropped: %#v", r.Env)
	}
	if r.Env["PROJ"] != "p" {
		t.Errorf("project env dropped: %#v", r.Env)
	}
}

func TestEnvRoundTripJSON(t *testing.T) {
	in := []byte(`{"env":{"FOO":"bar"},"enabledPlugins":{"p@m":true}}`)
	var s Settings
	if err := json.Unmarshal(in, &s); err != nil {
		t.Fatal(err)
	}
	if s.Env["FOO"] != "bar" {
		t.Fatalf("env not unmarshaled: %#v", s.Env)
	}
	out, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var back Settings
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}
	if back.Env["FOO"] != "bar" {
		t.Fatalf("env not marshaled: %s", out)
	}
}

func TestConfigToSettingsEnv(t *testing.T) {
	cfg := &config.Config{}
	cfg.Claude.Env = map[string]string{"X": "1"}
	s := ConfigToSettings(cfg)
	if s.Env["X"] != "1" {
		t.Fatalf("claude.env not mapped: %#v", s.Env)
	}
}
```

(Ensure `encoding/json` and `config` are imported in the test file; they already are in `settings_test.go`.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/providers/claude/ -run 'TestEnvMergePrecedence|TestEnvRoundTripJSON|TestConfigToSettingsEnv' -v`
Expected: FAIL — `Settings.Env` field undefined (compile error).

- [ ] **Step 3a: Add the field**

In `internal/providers/claude/settings.go`, add to the `Settings` struct (after `SkipDangerousModePermissionPrompt`, before `RawExtras`):

```go
	// Env is Claude Code's native settings.json "env" block. Merged across
	// all sources (host ~/.claude/settings.json, moat-user, project,
	// moat.yaml claude.env) — unlike RawExtras, host-source env survives.
	Env map[string]string `json:"env,omitempty"`
```

- [ ] **Step 3b: Register as a known key**

In `knownSettingsKeys` add the line:

```go
	"env":                               true,
```

- [ ] **Step 3c: Marshal it**

In `MarshalJSON`, after the `SkipDangerousModePermissionPrompt` block and before the `RawExtras` loop, add:

```go
	if len(s.Env) > 0 {
		m["env"] = s.Env
	}
```

(`UnmarshalJSON` needs no change — it unmarshals known fields via the struct alias, and `env` is now a struct field.)

- [ ] **Step 3d: Merge it (both branches of `MergeSettings`)**

In `MergeSettings`, in the `if base == nil {` branch, add `Env` to the cloned result struct literal:

```go
		result := &Settings{
			EnabledPlugins:                    cloneMapStringBool(override.EnabledPlugins),
			ExtraKnownMarketplaces:            cloneMapStringMarketplace(override.ExtraKnownMarketplaces),
			SkipDangerousModePermissionPrompt: override.SkipDangerousModePermissionPrompt,
			Env:                               cloneMapStringString(override.Env),
			PluginSources:                     make(map[string]SettingSource),
			MarketplaceSources:                make(map[string]SettingSource),
		}
```

In the main merge path (after the `result := &Settings{...}` literal that sets `SkipDangerousModePermissionPrompt`), add env union (override wins per key):

```go
	// Merge env: union all sources, override wins per key (precedence is
	// established by LoadAllSettings call order).
	if len(base.Env) > 0 || len(override.Env) > 0 {
		result.Env = make(map[string]string, len(base.Env)+len(override.Env))
		for k, v := range base.Env {
			result.Env[k] = v
		}
		for k, v := range override.Env {
			result.Env[k] = v
		}
	}
```

Place that block right after the marketplace override loop and before the `RawExtras` propagation block.

- [ ] **Step 3e: Add the clone helper**

At the end of `settings.go` (next to the other `cloneMap*` helpers):

```go
// cloneMapStringString returns a shallow copy of the map (nil-safe).
func cloneMapStringString(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
```

- [ ] **Step 3f: Map `claude.env` in `ConfigToSettings`**

In `ConfigToSettings`, before `return settings`, add:

```go
	if len(cfg.Claude.Env) > 0 {
		settings.Env = make(map[string]string, len(cfg.Claude.Env))
		for k, v := range cfg.Claude.Env {
			settings.Env[k] = v
		}
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/providers/claude/ -run 'TestEnvMergePrecedence|TestEnvRoundTripJSON|TestConfigToSettingsEnv' -v`
Expected: PASS

- [ ] **Step 5: Run full claude package tests (no regressions)**

Run: `go test ./internal/providers/claude/`
Expected: ok (no regressions in existing settings/merge tests)

- [ ] **Step 6: Commit**

```bash
git add internal/providers/claude/settings.go internal/providers/claude/settings_test.go
git commit -m "feat(claude): make settings.json env a first-class merged field"
```

---

## Task 5: AWS credential endpoint — `?format=claude` envelope

**Files:**
- Modify: `internal/providers/aws/endpoint.go` (`ServeHTTP` ~80-116)
- Test: `internal/providers/aws/endpoint_test.go` (create if it does not exist)

- [ ] **Step 1: Write the failing test**

If `internal/providers/aws/endpoint_test.go` does not exist, create it with `package aws` and these imports: `encoding/json`, `net/http/httptest`, `testing`, `time`, `context`, and `github.com/aws/aws-sdk-go-v2/service/sts`, `github.com/aws/aws-sdk-go-v2/aws`. Add:

```go
type fakeSTS struct{}

func (fakeSTS) AssumeRole(ctx context.Context, in *sts.AssumeRoleInput, _ ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
	exp := time.Now().Add(time.Hour)
	return &sts.AssumeRoleOutput{Credentials: &ststypes(in, exp)}, nil
}

func TestEndpointClaudeFormat(t *testing.T) {
	h := &EndpointHandler{cfg: &Config{RoleARN: "arn:aws:iam::1:role/r", Region: "us-west-2", SessionDuration: time.Hour}}
	h.SetSTSClient(fakeSTS{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/_aws/credentials?format=claude", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Credentials struct {
			AccessKeyId, SecretAccessKey, SessionToken string
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if got.Credentials.AccessKeyId == "" || got.Credentials.SessionToken == "" {
		t.Errorf("claude envelope missing creds: %s", rec.Body.String())
	}
}

func TestEndpointDefaultFormatUnchanged(t *testing.T) {
	h := &EndpointHandler{cfg: &Config{RoleARN: "arn:aws:iam::1:role/r", Region: "us-west-2", SessionDuration: time.Hour}}
	h.SetSTSClient(fakeSTS{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/_aws/credentials", nil))
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["Version"] == nil || got["AccessKeyId"] == nil {
		t.Errorf("default credential_process shape changed: %s", rec.Body.String())
	}
}
```

Add this helper in the same file (constructs an STS credentials value):

```go
func ststypes(_ *sts.AssumeRoleInput, exp time.Time) ststypesCreds { //nolint
	return ststypesCreds{exp: exp}
}
```

NOTE: implementing the fake by hand is brittle. Instead, check how `internal/providers/aws/endpoint_test.go` / `credential_provider_test.go` already fake STS (`SetSTSClient` + `STSAssumeRoler`). **Before writing the test, read `internal/providers/aws/credential_provider_test.go`** and reuse its existing STS fake/credentials-construction pattern rather than the placeholder `ststypes` above. Replace the fake accordingly so it returns a populated `*sts.AssumeRoleOutput` (with `Credentials.AccessKeyId/SecretAccessKey/SessionToken/Expiration`). The two test bodies (`TestEndpointClaudeFormat`, `TestEndpointDefaultFormatUnchanged`) stay as written.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/providers/aws/ -run 'TestEndpointClaudeFormat|TestEndpointDefaultFormatUnchanged' -v`
Expected: FAIL — `?format=claude` returns the default `Version/AccessKeyId` shape, so `TestEndpointClaudeFormat` fails (`Credentials` empty).

- [ ] **Step 3: Implement the envelope**

In `internal/providers/aws/endpoint.go`, replace the response-construction block in `ServeHTTP` (the `// AWS credential_process format` comment through the `json.NewEncoder(w).Encode(resp)` call) with:

```go
	w.Header().Set("Content-Type", "application/json")

	if r.URL.Query().Get("format") == "claude" {
		// Claude Code awsCredentialExport envelope (spec §3.0). No Version /
		// Expiration fields; refresh cadence is governed by
		// CLAUDE_CODE_API_KEY_HELPER_TTL_MS, not Expiration.
		resp := map[string]interface{}{
			"Credentials": map[string]interface{}{
				"AccessKeyId":     creds.AccessKeyID,
				"SecretAccessKey": creds.SecretAccessKey,
				"SessionToken":    creds.SessionToken,
			},
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			ui.Warnf("Failed to encode AWS credentials response: %v", err)
		}
		return
	}

	// AWS credential_process format
	// See: https://docs.aws.amazon.com/cli/latest/userguide/cli-configure-sourcing-external.html
	resp := map[string]interface{}{
		"Version":         1,
		"AccessKeyId":     creds.AccessKeyID,
		"SecretAccessKey": creds.SecretAccessKey,
		"SessionToken":    creds.SessionToken,
		"Expiration":      creds.Expiration.Format(time.RFC3339),
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		// Response already started, can't send HTTP error. Log and continue.
		ui.Warnf("Failed to encode AWS credentials response: %v", err)
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/providers/aws/ -run 'TestEndpointClaudeFormat|TestEndpointDefaultFormatUnchanged' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/providers/aws/endpoint.go internal/providers/aws/endpoint_test.go
git commit -m "feat(aws): add ?format=claude credential envelope for awsCredentialExport"
```

---

## Task 6: Credential helper — `--claude` mode

**Files:**
- Modify: `internal/providers/aws/credential_helper.go`
- Test: `internal/providers/aws/credential_helper_test.go` (create if absent)

- [ ] **Step 1: Write the failing test**

Create/append `internal/providers/aws/credential_helper_test.go`:

```go
package aws

import (
	"strings"
	"testing"
)

func TestCredentialHelperClaudeArg(t *testing.T) {
	s := CredentialHelperScript
	// The script must append ?format=claude to the URL when invoked with --claude.
	if !strings.Contains(s, "--claude") {
		t.Error("helper script does not handle --claude argument")
	}
	if !strings.Contains(s, "format=claude") {
		t.Error("helper script does not append format=claude query param")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/providers/aws/ -run TestCredentialHelperClaudeArg -v`
Expected: FAIL — script has no `--claude` handling.

- [ ] **Step 3: Add `--claude` handling to the script**

In `internal/providers/aws/credential_helper.go`, in `CredentialHelperScript`, insert this block immediately after the `MOAT_AWS_CREDENTIAL_URL not set` check closes (i.e. after the `fi` that ends the `if [ -z "$MOAT_AWS_CREDENTIAL_URL" ]` block, before the `TMPWORK=$(mktemp ...)` line):

```sh
if [ "$1" = "--claude" ]; then
  case "$MOAT_AWS_CREDENTIAL_URL" in
    *\?*) MOAT_AWS_CREDENTIAL_URL="$MOAT_AWS_CREDENTIAL_URL&format=claude" ;;
    *) MOAT_AWS_CREDENTIAL_URL="$MOAT_AWS_CREDENTIAL_URL?format=claude" ;;
  esac
fi
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/providers/aws/ -run TestCredentialHelperClaudeArg -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/providers/aws/credential_helper.go internal/providers/aws/credential_helper_test.go
git commit -m "feat(aws): add --claude mode to credential helper script"
```

---

## Task 7: Manager wiring — hoist settings load

**Why:** the AWS credential block (`manager.go` ~1188) runs *before* `claude.LoadAllSettings` (~1613). To resolve `AWS_PROFILE`/`AWS_REGION` from the merged Claude `env` for the AWS config file, the load must happen before the AWS block. `LoadAllSettings` is pure (reads files only; inputs `opts.Workspace`, `opts.Config` are immutable here), so hoisting is safe.

**Files:**
- Modify: `internal/run/manager.go` (the `var claudeSettings *claude.Settings` block at ~1610-1618; the AWS block at ~1188)

- [ ] **Step 1: Locate both points**

Run: `grep -n 'claudeSettings, loadErr = claude.LoadAllSettings\|if r.AWSCredentialProvider != nil {' internal/run/manager.go`
Expected: one LoadAllSettings line (~1613) with a line number greater than the AWS block line (~1188), confirming the ordering problem.

- [ ] **Step 2: Move the settings-load block above the AWS block**

Cut this exact block (lines ~1604-1618):

```go
	// Load merged Claude settings which includes:
	// - ~/.claude/plugins/known_marketplaces.json (marketplace URLs)
	// - ~/.claude/settings.json (enabled plugins)
	// - ~/.moat/claude/settings.json (moat user defaults)
	// - <workspace>/.claude/settings.json (project settings)
	// - moat.yaml claude.* fields (run overrides)
	var claudeSettings *claude.Settings
	if opts.Config != nil {
		var loadErr error
		claudeSettings, loadErr = claude.LoadAllSettings(opts.Workspace, opts.Config)
		if loadErr != nil {
			cleanupDaemonRun()
			return nil, fmt.Errorf("loading Claude settings: %w", loadErr)
		}
	}
```

Paste it verbatim immediately **before** the line:

```go
		// Set up AWS credential_process if AWS grant is active
```

(i.e. just before `if r.AWSCredentialProvider != nil {`'s preceding comment at ~1185). Delete the original location's now-removed block (the later "// Load merged Claude settings" comment region).

- [ ] **Step 3: Verify it still builds and the later usage still compiles**

Run: `go build ./internal/run/`
Expected: exit 0 (the `claudeSettings` variable is now declared earlier; its later use at ~1964 still resolves).

- [ ] **Step 4: Run manager/run tests**

Run: `go test ./internal/run/ 2>&1 | tail -5`
Expected: ok (no behavior change yet — pure hoist).

- [ ] **Step 5: Commit**

```bash
git add internal/run/manager.go
git commit -m "refactor(run): hoist Claude settings load above AWS credential block"
```

---

## Task 8: Manager wiring — Bedrock region, profile, env, settings gate

**Files:**
- Modify: `internal/run/manager.go` (AWS block ~1188-1245; settings.json write gate ~2058-2059; `claudeConfig.Env` merge ~2054)

This task has no standalone unit test (it is glue across a 4000-line file); it is covered by Task 9's dry-run assertion and by the unit tests of the helpers it calls. Build + existing run tests must stay green.

- [ ] **Step 1: Add a Bedrock-enabled helper near `hasGrant` (~line 4436)**

```go
// bedrockEnabled reports whether moat.yaml turns on Claude→Bedrock routing.
func bedrockEnabled(cfg *config.Config) bool {
	return cfg != nil && cfg.Claude.Bedrock != nil && cfg.Claude.Bedrock.Enabled
}
```

- [ ] **Step 2: Resolve region + profile inside the AWS block**

In `internal/run/manager.go`, inside `if r.AWSCredentialProvider != nil {`, replace the AWS-config-file construction:

```go
			// Write AWS config file
			awsConfig := fmt.Sprintf(`[default]
credential_process = /moat/aws/credentials
region = %s
`, r.AWSCredentialProvider.Region())
```

with:

```go
			// Resolve effective region: claude.bedrock.region (moat.yaml) >
			// merged settings env AWS_REGION > AWS grant region.
			region := r.AWSCredentialProvider.Region()
			if claudeSettings != nil && claudeSettings.Env["AWS_REGION"] != "" {
				region = claudeSettings.Env["AWS_REGION"]
			}
			if bedrockEnabled(opts.Config) && opts.Config.Claude.Bedrock.Region != "" {
				region = opts.Config.Claude.Bedrock.Region
			}

			// Honor AWS_PROFILE from merged settings env: write the
			// credential_process under [profile <name>] (plus a [default]
			// alias for SDK robustness). Default to [default] when unset.
			awsProfile := ""
			if claudeSettings != nil {
				awsProfile = claudeSettings.Env["AWS_PROFILE"]
			}
			var awsConfig string
			if awsProfile != "" {
				awsConfig = fmt.Sprintf(`[default]
credential_process = /moat/aws/credentials
region = %s

[profile %s]
credential_process = /moat/aws/credentials
region = %s
`, region, awsProfile, region)
			} else {
				awsConfig = fmt.Sprintf(`[default]
credential_process = /moat/aws/credentials
region = %s
`, region)
			}
```

- [ ] **Step 3: Use the resolved region for `AWS_REGION` env**

In the same block, change:

```go
				"AWS_REGION="+r.AWSCredentialProvider.Region(),
```

to:

```go
				"AWS_REGION="+region,
```

- [ ] **Step 4: Inject Bedrock env when enabled**

Immediately after the `proxyEnv = append(proxyEnv, ...)` call that sets `AWS_CONFIG_FILE`/`AWS_REGION`/etc. (and after the `MOAT_AWS_CREDENTIAL_TOKEN` conditional), add:

```go
			if bedrockEnabled(opts.Config) {
				proxyEnv = append(proxyEnv, claude.BedrockEnv(*opts.Config.Claude.Bedrock)...)
				proxyEnv = append(proxyEnv, "CLAUDE_CODE_API_KEY_HELPER_TTL_MS="+claude.BedrockTTLMillis())
			}
```

(`claude` is already imported in manager.go — see import at line 45.)

- [ ] **Step 5: Set `awsCredentialExport` + strip `awsAuthRefresh` in the merged settings (Bedrock mode)**

Find the settings.json write region (~2056-2077). Immediately before `settingsPath := filepath.Join(claudeConfig.StagingDir, "settings.json")`, the code does `if claudeSettings == nil { claudeSettings = &claude.Settings{} }`. After that nil-guard and the `claudeSettings.SkipDangerousModePermissionPrompt = skipPrompt` line, add:

```go
				if bedrockEnabled(opts.Config) {
					if claudeSettings.RawExtras == nil {
						claudeSettings.RawExtras = make(map[string]json.RawMessage)
					}
					// moat-managed: point Claude Code's awsCredentialExport at
					// the in-container helper (spec §3.0); override any host value.
					claudeSettings.RawExtras["awsCredentialExport"] =
						json.RawMessage(`"/moat/aws/credentials --claude"`)
					// Strip host awsAuthRefresh: it runs a host-only SSO command
					// that would fire inside the container on credential expiry.
					delete(claudeSettings.RawExtras, "awsAuthRefresh")
				}
```

- [ ] **Step 6: Widen the settings.json write gate to include Bedrock**

Change:

```go
			skipPrompt := opts.Config != nil && opts.Config.Claude.SkipPermissionsPrompt
			if hasPlugins || skipPrompt {
```

to:

```go
			skipPrompt := opts.Config != nil && opts.Config.Claude.SkipPermissionsPrompt
			if hasPlugins || skipPrompt || bedrockEnabled(opts.Config) {
```

- [ ] **Step 7: Suppress `ANTHROPIC_*` injection in Bedrock mode**

In `internal/providers/claude/agent.go`, change `PrepareContainer` so credential-derived env is skipped when Bedrock is on. Add an `opts.Bedrock bool` to `provider.PrepareOpts` (in `internal/provider/credential.go`, add field `Bedrock bool` with a doc comment), set it at the `claudeProvider.PrepareContainer(ctx, provider.PrepareOpts{...})` call site in manager.go (~2038) via `Bedrock: bedrockEnabled(opts.Config),`, and in `agent.go` replace:

```go
	env := containerEnvForCredential(opts.Credential)
	env = append(env, "MOAT_CLAUDE_INIT="+ClaudeInitMountPath)
```

with:

```go
	var env []string
	if !opts.Bedrock {
		// Bedrock authenticates via AWS; ANTHROPIC_API_KEY / base-URL relay
		// must not be set or it conflicts with CLAUDE_CODE_USE_BEDROCK.
		env = containerEnvForCredential(opts.Credential)
	}
	env = append(env, "MOAT_CLAUDE_INIT="+ClaudeInitMountPath)
```

- [ ] **Step 8: Build + test**

Run: `go build ./... && go test ./internal/run/ ./internal/providers/claude/ ./internal/provider/ 2>&1 | tail -8`
Expected: build exit 0; tests `ok`.

- [ ] **Step 9: Commit**

```bash
git add internal/run/manager.go internal/providers/claude/agent.go internal/provider/credential.go
git commit -m "feat(run): wire Claude Bedrock env, region, profile, and awsCredentialExport"
```

---

## Task 9: Strict-policy network hosts + end-to-end dry-run assertion

**Files:**
- Modify: `internal/providers/claude/cli.go` (`NetworkHosts` ~156-162 is static and has no config; instead add Bedrock hosts where the provider run config is assembled — see step 1)
- Test: `internal/run/` dry-run test (find the existing dry-run test with `grep -rn "DryRun" internal/run/*_test.go`)

- [ ] **Step 1: Add Bedrock hosts to the network allowlist**

`NetworkHosts()` takes no config, so add the Bedrock hosts in the run path where the resolved region is known. In `internal/run/manager.go`, locate where provider network hosts are appended to `cfg.Network.Rules` (run: `grep -n 'NetworkHosts\|NetworkRuleEntry\|Network.Rules' internal/run/manager.go internal/cli/provider.go`). In `internal/cli/provider.go`, the loop at ~162-166 converts `rc.NetworkHosts`/`rc.AllowedHosts` to rules. Add Bedrock hosts to `rc.NetworkHosts` before that loop:

```go
	if cfg != nil && cfg.Claude.Bedrock != nil && cfg.Claude.Bedrock.Enabled {
		region := cfg.Claude.Bedrock.Region
		if region == "" {
			region = "us-east-1" // documented default surface; actual creds region resolved in manager
		}
		rc.NetworkHosts = append(rc.NetworkHosts,
			"bedrock-runtime."+region+".amazonaws.com",
			"bedrock."+region+".amazonaws.com",
		)
	}
```

Place it immediately before the `for _, host := range append(rc.NetworkHosts, rc.AllowedHosts...)` loop. Confirm `cfg`/`config` is in scope there; if `ProviderRunConfig` does not carry the `*config.Config`, instead append these in `runClaudeCode`'s `ProviderRunConfig{...}` literal in `cli.go` by extending `NetworkHosts: NetworkHosts()` to a helper `bedrockNetworkHosts(claude config)` — read `internal/cli/provider.go` first to pick whichever site has the config available, and implement only one.

- [ ] **Step 2: Write the dry-run assertion test**

Find the dry-run test harness: `grep -rn "func Test.*DryRun\|DryRun:.*true\|--dry-run" internal/run/*_test.go internal/e2e/*.go 2>/dev/null | head`. Following that harness's existing pattern, add a test that builds a run with `agent: claude`, `grants: [aws]`, `claude.bedrock.enabled: true`, an AWS credential present (reuse the harness's existing AWS-credential test fixture), and asserts on the resolved container env/settings:

- `CLAUDE_CODE_USE_BEDROCK=1` is in the container env
- `ANTHROPIC_DEFAULT_SONNET_MODEL` is present
- `CLAUDE_CODE_API_KEY_HELPER_TTL_MS=300000` is present
- `ANTHROPIC_API_KEY` is **absent** even if an anthropic/claude credential also exists
- the staged `settings.json` contains `"awsCredentialExport":"/moat/aws/credentials --claude"`

If no existing dry-run harness exposes container env/settings, scope this step down to: a focused test in `internal/run/` that calls the smallest seam that returns `proxyEnv`/staged settings for a Bedrock config (document which seam in the test comment). Do not invent new production seams solely for testing — assert via the closest existing observable.

- [ ] **Step 3: Run the test to verify it fails, then passes**

Run: `go test ./internal/run/ -run Bedrock -v`
Expected: FAIL first (assertions unmet if any wiring is off), then PASS after confirming Task 8 wiring. If it passes immediately, that is acceptable — Task 8 already implemented the behavior; the test locks it in.

- [ ] **Step 4: Full touched-package suite + lint**

Run: `go build ./... && make test-unit ARGS='-run "Bedrock|Env|Settings|Claude"' 2>&1 | tail -10`
Then: `make lint 2>&1 | tail -10` (fall back to `go vet ./...` if golangci-lint is absent, per CLAUDE.md).
Expected: tests ok; lint clean.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/provider.go internal/providers/claude/cli.go internal/run/
git commit -m "feat(run): allow Bedrock hosts under strict policy + dry-run coverage"
```

---

## Task 10: Documentation + changelog

**Files:**
- Modify: `docs/content/reference/02-moat-yaml.md`
- Create: `docs/content/guides/<NN>-claude-bedrock.md` (use the next unused number prefix in that dir — run `ls docs/content/guides/`)
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Document the moat.yaml fields**

In `docs/content/reference/02-moat-yaml.md`, in the `claude:` section, add entries for `claude.env` (map; merged into the container's `~/.claude/settings.json` env block; precedence: moat.yaml > project > moat-user > host settings.json; moat-injected Bedrock vars always win) and `claude.bedrock` (`enabled`, `region`, `models.{haiku,sonnet,opus,custom}`; requires the `aws` grant; mutually exclusive with `base_url`/`llm-gateway`). Match the file's existing field-documentation format.

- [ ] **Step 2: Write the guide**

Create the guide (number-prefixed per existing convention). Cover: prerequisites (`grant aws <role-arn>`; IAM role needs `bedrock:InvokeModel`, `bedrock:InvokeModelWithResponseStream`, `bedrock:ListFoundationModels`); minimal `moat.yaml`; how credentials flow (awsCredentialExport → moat helper → STS endpoint, server-side refresh); model overrides; and a **copy-paste corp-hygiene `claude.env` block** containing the agentbox disable set:

```yaml
claude:
  env:
    CLAUDE_CODE_DISABLE_FEEDBACK_SURVEY: "1"
    CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: "1"
    CLAUDE_CODE_ENABLE_TELEMETRY: "0"
    DISABLE_AUTOUPDATER: "1"
    DISABLE_BUG_COMMAND: "1"
    DISABLE_ERROR_REPORTING: "1"
    DISABLE_EXTRA_USAGE_COMMAND: "1"
    DISABLE_FEEDBACK_COMMAND: "1"
    DISABLE_INSTALLATION_CHECKS: "1"
    DISABLE_INSTALL_GITHUB_APP_COMMAND: "1"
    DISABLE_LOGIN_COMMAND: "1"
    DISABLE_LOGOUT_COMMAND: "1"
    DISABLE_TELEMETRY: "1"
    DISABLE_UPGRADE_COMMAND: "1"
```

Note that live-Bedrock E2E is manual (requires a real role + model access). Follow `docs/STYLE-GUIDE.md` (objective, no marketing, working example first).

- [ ] **Step 3: Changelog**

In `CHANGELOG.md`, under the current unreleased version's `### Added`, add (PR number filled in at PR time):

```markdown
- **Claude on AWS Bedrock** — `claude.bedrock` in `moat.yaml` routes Claude Code through AWS Bedrock using the `aws` grant's STS role, via Claude Code's `awsCredentialExport` hook. Adds `claude.env` for merging arbitrary vars into Claude Code's `settings.json` env block (honors the host `~/.claude/settings.json` env). ([#NNN](https://github.com/majorcontext/moat/pull/NNN))
```

- [ ] **Step 4: Commit**

```bash
git add docs/ CHANGELOG.md
git commit -m "docs(claude): document claude.bedrock and claude.env"
```

---

## Final verification (run before opening a PR)

- [ ] `go build ./...` — exit 0
- [ ] `make test-unit` — full suite green with race detector
- [ ] `make lint` (or `go vet ./...`) — clean
- [ ] Manually re-read spec §3.0/§3.4/§3.6 and confirm: awsCredentialExport overrides host value; awsAuthRefresh stripped in Bedrock mode; env precedence moat.yaml > project > moat-user > host; AWS_PROFILE renders `[profile <name>]`.
- [ ] Use `superpowers:finishing-a-development-branch` to decide merge/PR.

---

## Self-Review (completed during planning)

**Spec coverage:** §3.0 awsCredentialExport → Tasks 5,6,8. §3.1 config → Task 1. §3.2 BedrockEnv → Task 3. §3.3/§3.4 settings env merge + precedence → Task 4 (+7 for ordering). §3.5 region resolver → Task 8. §3.6 AWS_PROFILE → Task 8. §3.7 validation/suppress ANTHROPIC_* → Tasks 2,8. §3.8 network → Task 9. §3.9 IAM → Task 10 docs. §3.10 fallbacks → not implemented (correctly: primary path only; documented as fallback in spec). Testing/docs → Tasks 9,10. **No gaps.**

**Placeholder scan:** Task 5's `ststypes` placeholder is explicitly flagged with a read-first instruction to reuse the existing STS fake; Task 9 step 1/2 explicitly say "read X first, implement only one site" because the exact seam depends on `ProviderRunConfig` shape. These are bounded investigation steps, not unspecified work. `<NN>`/`#NNN` are intentional (next-free-number / PR-number-at-PR-time).

**Type consistency:** `config.BedrockConfig`/`BedrockModels`/`ClaudeConfig.Env`/`ClaudeConfig.Bedrock` (Task 1) used identically in Tasks 3,8,9. `BedrockEnv(config.BedrockConfig) []string`, `BedrockTTLMillis() string`, `defaultBedrockModels` (Task 3) match Task 8 calls. `Settings.Env`, `cloneMapStringString`, `bedrockEnabled(*config.Config)` consistent across Tasks 4,8,9. `PrepareOpts.Bedrock bool` defined and used in Task 8.
