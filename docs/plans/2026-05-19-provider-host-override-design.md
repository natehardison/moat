# Provider host override on `moat grant`

**Status:** Draft
**Date:** 2026-05-19

## Problem

The GitLab credential provider is shipped as embedded YAML (`internal/providers/configprovider/defaults/gitlab.yaml`) with hardcoded `gitlab.com` and `*.gitlab.com` hosts and a `https://gitlab.com/api/v4/user` validate URL. Users with self-hosted GitLab instances (e.g., `gitlab.acme.com`) cannot grant a credential for their own host without hand-authoring a full YAML override at `~/.moat/providers/gitlab.yaml`.

That override mechanism already works — `loader.go:73-106` reads user YAML files and replaces embedded defaults by name — but it is undocumented in the grant flow, requires the user to learn the YAML schema, and offers no discoverability from the CLI.

A similar need exists for tenant-scoped SaaS providers (e.g., a future Atlassian provider with `<tenant>.atlassian.net`). Building per-credential host metadata or generic `${host}` templating in YAML would address both, but is more code than this user's stated workflow needs ("just the one self-hosted instance").

## Goal

Add `--host <hostname>` to `moat grant <provider>`. When supplied for a YAML-defined provider, the flag generates a user override at `~/.moat/providers/<name>.yaml` with the host substituted into the hosts list and the validate URL, then runs the normal grant flow using the overridden definition. Subsequent `moat run --grant <name>` invocations pick up the host automatically because the loader already prefers user YAML over embedded YAML.

Non-goal: per-credential host metadata, per-run host overrides, host templating in YAML, custom ports, Go-provider host customization.

## User flow

```
$ moat grant gitlab --host gitlab.acme.com
Writing provider override to ~/.moat/providers/gitlab.yaml
Enter a GitLab personal access token.
Token: ****
Validating token...
Token validated successfully
Credential saved to ~/.moat/credentials/gitlab.enc
```

Subsequent `moat grant gitlab` (no flag) preserves the override; only the token is re-prompted. Rotating the token does not touch the override.

To change the host: re-run `moat grant gitlab --host newhost.example.com`. A confirmation prompt fires if the existing file differs from what would be written.

To remove the override: `rm ~/.moat/providers/gitlab.yaml`. This is documented in the grants reference.

## Design

### Code structure

**New file: `internal/providers/configprovider/override.go`**

```go
// LoadEmbeddedDef reads and parses the embedded YAML definition for the
// given provider name. Returns an error if no embedded default exists.
func LoadEmbeddedDef(name string) (ProviderDef, error)

// ApplyHostOverride returns a copy of def with Hosts replaced by [host]
// and Validate.URL rewritten so its host component matches the user's host.
// Pure function — no I/O.
func ApplyHostOverride(def ProviderDef, host string) (ProviderDef, error)

// UserOverridePath returns the canonical path for a provider's user-level
// override YAML (~/.moat/providers/<name>.yaml).
func UserOverridePath(name string) string

// WriteUserOverride marshals def to YAML and writes it to
// UserOverridePath(name), creating the providers directory if needed.
func WriteUserOverride(name string, def ProviderDef) error
```

**Modified: `cmd/moat/cli/grant.go`**

- Add `--host <hostname>` (string) flag on `grantCmd`.
- In `runGrant`, after CLI-name remapping (`openai` → `codex`, `google` → `gemini`) and before the registry lookup:
  - If `--host` is empty: existing path, no changes.
  - If `--host` is set:
    1. Validate the hostname (see below).
    2. Call `configprovider.LoadEmbeddedDef(providerName)`. If it errors, report that `--host` is only supported for YAML-defined providers and list eligible names.
    3. Call `configprovider.ApplyHostOverride(def, host)`.
    4. Check for an existing file at `configprovider.UserOverridePath(providerName)`:
       - If absent: print `Writing provider override to <path>` and write the new override.
       - If present and identical: print `Override at <path> already set to <host> — no changes needed` and skip the write.
       - If present and different: print a one-line diff summary (old hosts → new host), prompt `Overwrite? [y/N]`. On `n` or empty/non-TTY, abort with exit code 1. On `y`, print `Updated provider override at <path>` and write.
    5. Construct a one-off `ConfigProvider` from the overridden def (`configprovider.NewConfigProvider(overridden, "custom")`) and call its `.Grant(ctx)` directly. This bypasses the global registry so token validation hits the user's host.
    6. Save the resulting credential via the existing `saveCredential` path.

The global registry is not touched. The override takes effect for future processes the moment the file is written; the current process uses the in-memory overridden def for the rest of this command.

### validate.url rewriting

`ApplyHostOverride` parses `def.Validate.URL` with `net/url`, replaces `u.Host` with the user-supplied host, and re-serializes. Scheme, path, query, and any `${token}` placeholders in the path are preserved.

If a provider has no `validate` block, the rewrite is skipped — only `hosts` changes.

If the validate URL parses but lacks a host (relative URL), return an error from `ApplyHostOverride` — defensive only; no built-in provider does this.

### Hostname validation

Reject anything that isn't a bare DNS hostname:

- No scheme prefix (`https://`, `http://`).
- No path or query suffix (`/anything`, `?anything`).
- No port (`:8080`). Custom ports are out of scope for v1.
- No userinfo.
- Must contain at least one dot — `localhost` is rejected because it's almost never what the user meant.
- Labels must conform to RFC 1123: lowercase letters, digits, hyphens; no leading or trailing hyphen; ≤63 chars per label; ≤253 chars total.

Implementation: try `url.Parse("https://" + host)`; if the parsed `Host` differs from the input, reject. Then split on `.` and regex-match each label.

Error format: `--host must be a bare hostname (e.g., gitlab.acme.com), got "<input>"`.

### Restricting `--host` to YAML providers

Detected by `LoadEmbeddedDef(name)` — if no embedded YAML exists for the resolved provider name, error with:

```
--host is not supported for "<name>" (built-in provider with a fixed host)
Supported providers: brave-search, datadog, elevenlabs, gitlab, linear, sentry, telegram, vercel
```

The first line names the provider the user supplied; the second line lists the eligible providers, sorted alphabetically.

The eligible list is built at runtime from `embed.FS`, so it stays accurate as YAML providers are added.

### Confirmation logic

When `~/.moat/providers/<name>.yaml` exists and is being overwritten:

1. Parse the existing file with `parseProviderDef`.
2. Compare `Hosts` and `Validate.URL` to the new override. If equal, no-op (print one line, continue to grant).
3. Otherwise, print:
   ```
   Existing override at ~/.moat/providers/gitlab.yaml: hosts=[gitlab.old.com]
   New override:                                        hosts=[gitlab.acme.com]
   Overwrite? [y/N]:
   ```
4. Read one line from stdin via the existing prompt helpers. On `y`/`Y` proceed; otherwise abort with `aborted: existing override not overwritten` and a hint.
5. If stdin is not a TTY (e.g., piped input), treat as `N`. No `--force` flag in v1.

If the existing file fails to parse, do not silently overwrite — error with `existing override at <path> is invalid YAML; remove or fix it before re-running`.

## File layout

```
internal/providers/configprovider/
  override.go               # new — host override helpers
  override_test.go          # new — unit tests for the helpers

cmd/moat/cli/
  grant.go                  # +--host flag, override branch in runGrant
  grant_test.go             # +cases for the new flag

docs/content/reference/
  01-cli.md                 # document --host on `moat grant`
  04-grants.md              # GitLab self-hosted subsection
  07-provider-yaml.md       # cross-reference the shortcut

CHANGELOG.md                # ### Added entry under next unreleased version
```

## Test plan

### Unit (`internal/providers/configprovider/override_test.go`)

- `LoadEmbeddedDef("gitlab")` returns a parsed def matching the embedded file.
- `LoadEmbeddedDef("github")` returns an error.
- `LoadEmbeddedDef("nonexistent")` returns an error.
- `ApplyHostOverride(gitlabDef, "gitlab.acme.com")`:
  - `Hosts == ["gitlab.acme.com"]`.
  - `Validate.URL == "https://gitlab.acme.com/api/v4/user"`.
  - All other fields equal the input.
- `ApplyHostOverride` on a def with no `Validate` leaves `Validate` nil and only changes hosts.
- `ApplyHostOverride` preserves `${token}` placeholders in URL paths.
- `WriteUserOverride` writes a file that round-trips through `parseProviderDef` and produces an equal def.
- `UserOverridePath("gitlab")` returns `<GlobalConfigDir>/providers/gitlab.yaml`.

### Hostname validation (same file or `grant_test.go`)

- Accept: `gitlab.acme.com`, `git.foo.bar.example`, `a-b.example.io`.
- Reject (each with a clear assertion message):
  - `https://gitlab.acme.com`
  - `gitlab.acme.com/path`
  - `gitlab.acme.com:8080`
  - `gitlab.acme.com?x=1`
  - `user@gitlab.acme.com`
  - `localhost`
  - empty string
  - `-leading.example.com`
  - `trailing-.example.com`
  - a label longer than 63 chars
  - a hostname longer than 253 chars

### CLI integration (`cmd/moat/cli/grant_test.go`)

- `--host gitlab.acme.com` on `github`: errors with the eligible-providers message; no file is written.
- `--host bad/host` on `gitlab`: errors before any file is written.
- `--host gitlab.acme.com` on `gitlab`, no existing user file: writes the file with expected contents, then proceeds to grant. (Mock the validate HTTP call.)
- `--host gitlab.acme.com` on `gitlab`, existing matching user file: skips write, prints the no-change line, proceeds to grant.
- `--host gitlab.new.com` on `gitlab`, existing different user file, stdin says `n`: aborts non-zero, file unchanged.
- `--host gitlab.new.com` on `gitlab`, existing different user file, stdin says `y`: overwrites file, proceeds to grant.

### E2E

Skipped for v1. Unit tests cover file generation; existing grant flow tests cover credential storage. Adding a full E2E (real HTTP mock, real binary invocation) is not justified for the size of this change.

## Documentation changes

- **`docs/content/reference/01-cli.md`** — Under `moat grant`, add `--host` to the flags table with description: "For YAML-defined providers, write a `~/.moat/providers/<name>.yaml` override that routes credential injection and token validation to the specified host." Add a `moat grant gitlab --host gitlab.acme.com` example under the existing grant examples.

- **`docs/content/reference/04-grants.md`** — Add a `Self-hosted GitLab` subsection under the GitLab grant docs:

  > For a self-hosted GitLab instance, supply `--host` when granting:
  >
  > ```bash
  > moat grant gitlab --host gitlab.acme.com
  > ```
  >
  > This writes `~/.moat/providers/gitlab.yaml`, which routes credential injection and token validation to your host. Subsequent `moat run --grant gitlab` uses the override automatically. To remove the override, delete `~/.moat/providers/gitlab.yaml`.

- **`docs/content/reference/07-provider-yaml.md`** — Add a one-paragraph note in the "Custom providers" section that `moat grant <name> --host` is shorthand for writing the user YAML manually, and link to the grants reference.

- **`CHANGELOG.md`** — Under the next unreleased version, in `### Added`:

  > **Self-hosted host override on `moat grant`** — `moat grant gitlab --host gitlab.acme.com` writes a user provider YAML that routes credential injection and validation to a custom host. Useful for self-hosted GitLab and similar deployments. Linked to PR.

## Out of scope

- New Atlassian provider — separate PR.
- `--host` on Go-implemented providers (github, anthropic, claude, openai/codex, gemini, npm, aws).
- Custom ports in `--host`.
- A `--force` flag to skip the overwrite prompt.
- Generic YAML templating (e.g., `${host}` placeholders in the embedded YAML).
- Per-credential host metadata or per-run host override flags.

## Risks and open questions

- **In-process loader cache.** `RegisterAll` uses `sync.Once`, so the loaded provider definitions are immutable within a process. The design sidesteps this by constructing a one-off `ConfigProvider` from the overridden def for this command's grant call. Future invocations get the override via normal loader startup. No change to `sync.Once` semantics is required.
- **Existing file detection vs. concurrent grants.** Two simultaneous `moat grant ... --host ...` invocations could race on the write. Not realistic in practice; ignore.
- **Provider names with aliases.** `--host` is keyed by the resolved provider name (`openai` → `codex`), so users running `moat grant openai --host ...` would write `codex.yaml`. Codex isn't host-customizable so this would error at `LoadEmbeddedDef`. Acceptable.
