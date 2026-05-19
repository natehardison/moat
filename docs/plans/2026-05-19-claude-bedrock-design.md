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

Decision (confirmed): **moat-native endpoint, delivered via Claude Code's
documented `awsCredentialExport` settings hook** (see §3.0). The AWS provider
already does `AssumeRole`, serves creds at `/_aws/credentials`, mounts a helper
at `/moat/aws/credentials`, and sets `AWS_REGION` / `AWS_CA_BUNDLE`
(`manager.go:1185-1245`). The missing pieces are (a) the env vars that tell
Claude Code to use Bedrock, (b) an `awsCredentialExport` hook + reshaping
helper, and (c) honoring/merging the host settings `env` block.

This path is **language-agnostic**: `awsCredentialExport` is an app-level
Claude Code hook, so it does **not** depend on the Rust AWS SDK honoring
`credential_process` or `AWS_CONTAINER_CREDENTIALS_FULL_URI`. That removes the
biggest unknown. `credential_process` / static creds remain as fallbacks
(§3.10) only if the hook proves unviable.

## Design

### 3.0 Credential delivery: `awsCredentialExport` hook

Officially documented (code.claude.com/docs/en/amazon-bedrock). Claude Code
runs the configured command at session start and on each reload, capturing
stdout silently, expecting:

```json
{ "Credentials": { "AccessKeyId": "...", "SecretAccessKey": "...", "SessionToken": "..." } }
```

Reload cadence is governed by `CLAUDE_CODE_API_KEY_HELPER_TTL_MS`.

moat writes `awsCredentialExport` into the container's
`~/.claude/settings.json` pointing at a small **in-container helper** that:

1. calls moat's existing AWS credential source (the `/_aws/credentials`
   endpoint via the mounted helper — server-side `AssumeRole` + caching, no
   change to that machinery), then
2. reshapes the `credential_process` JSON
   (`{"Version":1,"AccessKeyId":...,"SecretAccessKey":...,"SessionToken":...,"Expiration":...}`)
   into the `{"Credentials":{...}}` envelope Claude Code expects.

Implemented by extending the existing helper with a `--format claude` mode (or
a sibling helper), so there is one credential source with two output shapes.

`CLAUDE_CODE_API_KEY_HELPER_TTL_MS` is set to roughly half the resolved AWS
session duration (`MetaKeySessionDuration` / `DefaultSessionDuration`), with a
conservative floor (e.g. 300000 ms). The endpoint already returns fresh STS
creds per call, so a short TTL is safe and only affects how often the hook
re-runs.

**Host-key handling (Bedrock mode):** the user's host
`~/.claude/settings.json` may carry `awsCredentialExport` / `awsAuthRefresh`
pointing at host binaries absent in the container (this is exactly why
agentbox strips them). moat therefore, in Bedrock mode:

- **overrides** `awsCredentialExport` with the moat helper (moat-managed key;
  host value ignored), and
- **strips** `awsAuthRefresh` (a host SSO/browser flow; moat refreshes
  server-side, and a dangling host command would fire on credential expiry).

These two keys are moat-managed in Bedrock mode and are *not* subject to the
generic settings merge precedence (§3.4).

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

### 3.10 Fallbacks (only if the `awsCredentialExport` hook proves unviable)

The hook is officially documented and language-agnostic, so this is unlikely.
If it fails to behave as documented against the real binary, in order:

1. **`credential_process`** via the mounted `AWS_CONFIG_FILE` (moat's existing
   AWS machinery, unchanged) — works iff the Rust SDK honors it.
2. **Static creds file**: write STS keys directly into `AWS_CONFIG_FILE`
   (`aws_access_key_id` / `_secret_access_key` / `_session_token`, under
   `[default]` or `[profile <name>]` per §3.6) and refresh before expiry — the
   path agentbox empirically proves works (minus its dummy-key + proxy-re-sign
   layer, which we are not adding). Trade-off: short-lived real creds sit in a
   `0600` mounted file rather than being fetched on demand.

All fallbacks are contained to the `manager.go` AWS block; the rest of the
design is unchanged.

## Files to change

| File | Change |
|---|---|
| `internal/config/config.go` | `ClaudeConfig.Env`, `ClaudeConfig.Bedrock`, `BedrockConfig`, `BedrockModels`, `defaultBedrockModels()`, validation hooks |
| `internal/providers/claude/settings.go` | `Settings.Env` first-class, merge with precedence + source tracking; in Bedrock mode set moat-managed `awsCredentialExport` and drop host `awsAuthRefresh` (§3.0) |
| `internal/providers/claude/agent.go` | write merged `env` + `awsCredentialExport` into container `settings.json`; emit `CLAUDE_CODE_API_KEY_HELPER_TTL_MS`; suppress `ANTHROPIC_*` when Bedrock |
| `internal/providers/claude/bedrock.go` (new) | `BedrockEnv`, default model IDs |
| `internal/providers/claude/cli.go` | add Bedrock `NetworkHosts` under strict policy |
| `internal/providers/aws/` (helper) | extend the credential helper (`GetCredentialHelper`) with a `--format claude` mode emitting the `{"Credentials":{...}}` envelope (§3.0) |
| `internal/run/manager.go` | region resolver; `AWS_PROFILE`→`[profile]` config; append `BedrockEnv` to `proxyEnv` in the AWS block (`~1185-1245`) gated on `cfg.Claude.Bedrock.Enabled && r.AWSCredentialProvider != nil` |
| `internal/providers/aws/grant.go` | (read-only) reuse `ConfigFromCredential` region |

## Testing

- **Unit:** config parse + validation (missing aws grant, base_url/llm-gateway
  conflict); `Settings.Env` merge precedence incl. host-source survival;
  Bedrock-mode settings handling (moat-managed `awsCredentialExport` overrides
  host value; `awsAuthRefresh` stripped); credential helper `--format claude`
  envelope reshaping (incl. session token / error passthrough);
  `BedrockEnv` defaults vs. overrides; `AWS_PROFILE`→config rendering;
  region resolver precedence; `CLAUDE_CODE_API_KEY_HELPER_TTL_MS` derivation.
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

- **`awsCredentialExport` behavior vs. docs.** Documented and language-agnostic,
  so low risk, but the exact reshaped JSON and TTL behavior should be smoke-
  tested against the real binary early; fallbacks in §3.10.
- **`awsAuthRefresh` interaction.** If not stripped in Bedrock mode, a host SSO
  command would fire inside the container on credential expiry. Handled in §3.0;
  covered by a unit test.
- **Stale model IDs.** Mitigated by `claude.bedrock.models` overrides and
  documenting that defaults track agentbox's current set.
- **Host `env` trust.** moat will read the *entire* host `~/.claude/settings.json`
  `env` block (not allowlisted). Same trust boundary as the existing host
  `~/.claude.json` read; acceptable since it's the user's own host file.
  Bedrock core vars still win, so it can't disable Bedrock by accident.
- **`AWS_REGION` set globally** could surprise non-Bedrock AWS users; scoped by
  only resolving the Bedrock region path when `bedrock.enabled`.
