# Claude-on-AWS-Bedrock Support — Design

- **Date:** 2026-05-19
- **Status:** Approved (design); plan pending
- **Topic:** Route Claude Code through AWS Bedrock using moat's existing AWS credential path, matching the behavior of `/workspaces/agentbox`.

## Goal

Let a moat user run `agent: claude` against **AWS Bedrock** instead of the
Anthropic API, by reusing moat's existing AWS STS `AssumeRole` +
`credential_process` plumbing. Opt-in and configured declaratively in
`moat.yaml`. Honor the user's host `~/.claude/settings.json` `env` block
(including `AWS_PROFILE` / `AWS_REGION`), the way agentbox does.

## Non-goals

- SigV4 re-signing in the proxy / any change to `gatekeeper` (separate repo,
  not vendored). Agentbox's "dummy creds in container, proxy re-signs" model is
  explicitly **out of scope** — see Background.
- A baked-in telemetry/hardening toggle. Replaced by a generic `claude.env`.
- Live-Bedrock E2E automation (requires a real role + model access; left manual).

## Background: two credential models

| | Agentbox | moat (chosen) |
|---|---|---|
| Creds in container | Dummy `~/.aws/config`; proxy re-signs SigV4 | Real **short-lived STS** creds via `credential_process` (existing `/moat/aws/credentials` helper + `/_aws/credentials` endpoint) |
| Signing | Proxy (agentproxy `sigv4.py`) | Claude Code's bundled AWS SDK (Rust) signs; proxy MITM-observes via existing CA bundle |
| Proxy changes | N/A | **None** |
| Blast radius | Would need gatekeeper changes | Additive: `internal/config`, `internal/providers/claude`, one gated block in `internal/run/manager.go` |

> **Pre-implementation verification (blocking).** Claude Code is a Rust
> binary. agentbox proves it reads **static keys** from `~/.aws/config` /
> `AWS_PROFILE`, but **not** that its bundled AWS SDK for Rust honors
> `credential_process` or `AWS_CONTAINER_CREDENTIALS_FULL_URI` — which is what
> moat's existing AWS path relies on. The plan's first step must verify this
> against the actual Claude Code binary (e.g. a throwaway run with a
> `credential_process` that logs invocation). If unsupported, use the
> **static-creds fallback** (3.10).

Decision (confirmed): **moat-native endpoint**. The AWS provider already does
`AssumeRole`, serves creds at `/_aws/credentials`, mounts the
`credential_process` helper at `/moat/aws/credentials`, and sets
`AWS_CONFIG_FILE` / `AWS_REGION` / `AWS_CA_BUNDLE` (`manager.go:1185-1245`).
The only missing pieces are (a) the env vars that tell Claude Code to use
Bedrock, and (b) honoring the host settings `env` block.

## Design

### 3.1 Config schema (`internal/config/config.go`)

Add to `ClaudeConfig`:

```go
// Env is merged into the container's ~/.claude/settings.json "env" block.
// Generic passthrough (mirrors Claude Code's native settings.json env).
// Use it for corp hygiene vars (telemetry/autoupdater off), AWS_REGION, etc.
Env map[string]string `yaml:"env,omitempty"`

// Bedrock routes Claude Code through AWS Bedrock instead of the Anthropic
// API. Requires the "aws" grant. nil = disabled.
Bedrock *BedrockConfig `yaml:"bedrock,omitempty"`
```

```go
type BedrockConfig struct {
	Enabled bool          `yaml:"enabled,omitempty"`
	Region  string        `yaml:"region,omitempty"` // optional override
	Models  BedrockModels `yaml:"models,omitempty"`
}

type BedrockModels struct {
	Haiku  string `yaml:"haiku,omitempty"`
	Sonnet string `yaml:"sonnet,omitempty"`
	Opus   string `yaml:"opus,omitempty"`
	Custom string `yaml:"custom,omitempty"`
}
```

Built-in defaults (a `defaultBedrockModels()` constructor), mirroring
agentbox's current set; any subset overridable:

| Var | Default | Name var |
|---|---|---|
| `ANTHROPIC_DEFAULT_HAIKU_MODEL` | `global.anthropic.claude-haiku-4-5-20251001-v1:0` | `Haiku 4.5` |
| `ANTHROPIC_DEFAULT_SONNET_MODEL` | `global.anthropic.claude-sonnet-4-6[1m]` | `Sonnet 4.6` |
| `ANTHROPIC_DEFAULT_OPUS_MODEL` | `global.anthropic.claude-opus-4-6-v1[1m]` | `Opus 4.6` |
| `ANTHROPIC_CUSTOM_MODEL_OPTION` | `global.anthropic.claude-opus-4-7[1m]` | `Opus 4.7` |

Model ID strings (incl. the `[1m]` modifier) pass through verbatim.

### 3.2 Bedrock env helper (`internal/providers/claude`)

`func BedrockEnv(bc BedrockConfig) []string` returns the **moat-injected core
vars** (highest precedence — see 3.4):

- `CLAUDE_CODE_USE_BEDROCK=1`
- the four `ANTHROPIC_DEFAULT_*_MODEL` / `ANTHROPIC_CUSTOM_MODEL_OPTION` vars
  plus their `_NAME` companions
- `AWS_SDK_UA_APP_ID=ClaudeCode-Sandbox` (attribution; harmless)

Deliberately **not** ported: `AWS_SDK_LOAD_CONFIG=1` — it is an AWS SDK for
**Go v1** flag with no effect on Claude Code's bundled **Rust** AWS SDK, which
reads `~/.aws/config` (and the credential chain) unconditionally.

### 3.3 Host settings `env` block becomes first-class (`internal/providers/claude/settings.go`)

Today `env` is an unknown field captured in `RawExtras`, and only RawExtras
from the **moat-user** source survive merge (`settings.go:47-52`) — so the
host `~/.claude/settings.json` `env` block is currently **dropped**. Change:

- Add `Env map[string]string \`json:"env,omitempty"\`` to `Settings`; add
  `"env"` to `knownSettingsKeys`; handle it in custom (Un)MarshalJSON.
- Merge `env` across all sources (host `~/.claude/settings.json`, moat-user
  `~/.moat/claude/settings.json`, project `.claude/settings.json`) with
  per-key source tracking, exempt from the moat-user-only RawExtras rule.

### 3.4 Env precedence (confirmed)

Per-key, highest wins:

1. **moat-injected Bedrock core vars** (`CLAUDE_CODE_USE_BEDROCK`, model IDs) — always win
2. `moat.yaml` `claude.env`
3. project `.claude/settings.json` `env`
4. moat-user `~/.moat/claude/settings.json` `env`
5. host `~/.claude/settings.json` `env` (lowest, but still honored)

The merged result is written into the container's `~/.claude/settings.json`
`env` block by the Claude provider's `PrepareContainer` (`agent.go`). Bedrock
core vars are emitted as process env via `proxyEnv` from `manager.go`; both
surfaces are read by Claude Code.

### 3.5 Region resolution

Effective region, highest wins: `claude.bedrock.region` → merged-env
`AWS_REGION` (resolved via 3.4) → AWS grant metadata region
(`MetaKeyRegion`) → `DefaultRegion`. Factor a single resolver. The effective
region drives: process `AWS_REGION`, `/moat/aws/config` `region=`, and the
strict-policy host allowlist (3.7).

### 3.6 AWS_PROFILE handling (confirmed: honor it)

moat writes `credential_process` under `[default]` today. If the merged `env`
sets `AWS_PROFILE=foo`:

- Render `/moat/aws/config` under `[profile foo]` (plus a `[default]` alias for
  SDK robustness) instead of bare `[default]`, and keep `AWS_PROFILE=foo` in
  the container env.
- When `AWS_PROFILE` is unset, behavior is unchanged (`[default]`).

This lives in the `manager.go` AWS block where the config file is written
(`manager.go:1207-1217`); it needs read access to the merged Claude `env`.

### 3.7 Validation & mutual exclusivity

At config-validate / run-create:

- `bedrock.enabled` **requires** an `aws` grant → actionable error:
  `Bedrock mode needs grants: [aws]; run 'moat grant aws <role-arn>'`.
- **Mutually exclusive** with `claude.base_url` and `claude.llm-gateway`
  (both set `ANTHROPIC_BASE_URL`; Bedrock is a different auth path) → error.
- A `claude`/`anthropic` credential grant is **not required** and **not used**
  under Bedrock. If present: warn it's superseded, and **suppress**
  `ANTHROPIC_API_KEY` / `ANTHROPIC_BASE_URL` injection (otherwise it conflicts
  with `CLAUDE_CODE_USE_BEDROCK`). This gates `containerEnvForCredential`
  (`agent.go:144-152`) and the base-URL relay wiring.

### 3.8 Network policy

`permissive` (the default here): nothing extra. `strict`: the Claude provider
adds `bedrock-runtime.<region>.amazonaws.com` and
`bedrock.<region>.amazonaws.com` (model listing) to its `NetworkHosts` when
Bedrock is enabled (region from 3.5). AWS traffic already goes through the
proxy with the moat CA bundle, so no TLS changes.

### 3.9 IAM

The granted role must allow `bedrock:InvokeModel` /
`bedrock:InvokeModelWithResponseStream` (and `bedrock:ListFoundationModels`
for the model picker). Documented, not enforced by moat.

### 3.10 Static-creds fallback (only if `credential_process` unsupported)

If verification shows Claude Code's Rust SDK ignores `credential_process` /
the container endpoint, moat writes the STS credentials directly into the
mounted `AWS_CONFIG_FILE` as `aws_access_key_id` / `aws_secret_access_key` /
`aws_session_token` (under `[default]` or `[profile <name>]` per 3.6) and
refreshes the file before expiry — the path agentbox empirically proves works
(minus its dummy-key + proxy-re-sign layer, which we are not adding). This is
contained to the `manager.go` AWS block; the rest of the design is unchanged.
Trade-off: short-lived real creds sit in a `0600` mounted file rather than
being fetched on demand.

## Files to change

| File | Change |
|---|---|
| `internal/config/config.go` | `ClaudeConfig.Env`, `ClaudeConfig.Bedrock`, `BedrockConfig`, `BedrockModels`, `defaultBedrockModels()`, validation hooks |
| `internal/providers/claude/settings.go` | `Settings.Env` first-class, merge with precedence + source tracking |
| `internal/providers/claude/agent.go` | write merged `env` into container `settings.json`; suppress `ANTHROPIC_*` when Bedrock |
| `internal/providers/claude/bedrock.go` (new) | `BedrockEnv`, default model IDs |
| `internal/providers/claude/cli.go` | add Bedrock `NetworkHosts` under strict policy |
| `internal/run/manager.go` | region resolver; `AWS_PROFILE`→`[profile]` config; append `BedrockEnv` to `proxyEnv` in the AWS block (`~1185-1245`) gated on `cfg.Claude.Bedrock.Enabled && r.AWSCredentialProvider != nil` |
| `internal/providers/aws/grant.go` | (read-only) reuse `ConfigFromCredential` region |

## Testing

- **Unit:** config parse + validation (missing aws grant, base_url/llm-gateway
  conflict); `Settings.Env` merge precedence incl. host-source survival;
  `BedrockEnv` defaults vs. overrides; `AWS_PROFILE`→config rendering;
  region resolver precedence.
- **Manager:** dry-run env assertion that Bedrock vars + resolved
  `AWS_REGION` + profile-aware AWS config appear, and that `ANTHROPIC_API_KEY`
  is absent when Bedrock + anthropic grant coexist.
- **E2E:** live Bedrock left manual (real role + model access); documented in
  the guide.

## Docs

- `docs/content/reference/02-moat-yaml.md` — `claude.bedrock` + `claude.env`.
- New `docs/content/guides/NN-claude-bedrock.md` — setup, IAM policy snippet,
  and a **copy-paste agentbox hygiene `claude.env` block** (the ~15
  telemetry/autoupdater/feedback `DISABLE_*` vars) for corp environments.
- `CHANGELOG.md` — **Added**, linked to the PR.

## Risks / open items

- **`credential_process` support in the Rust binary (highest risk).** See the
  blocking verification note above; fallback in 3.10. Resolve before building.
- **Stale model IDs.** Mitigated by `claude.bedrock.models` overrides and
  documenting that defaults track agentbox's current set.
- **Host `env` trust.** moat will read the *entire* host `~/.claude/settings.json`
  `env` block (not allowlisted). Same trust boundary as the existing host
  `~/.claude.json` read; acceptable since it's the user's own host file.
  Bedrock core vars still win, so it can't disable Bedrock by accident.
- **`AWS_REGION` set globally** could surprise non-Bedrock AWS users; scoped by
  only resolving the Bedrock region path when `bedrock.enabled`.
