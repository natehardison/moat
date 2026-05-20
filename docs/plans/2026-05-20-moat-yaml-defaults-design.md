# Per-User `moat.yaml` Defaults — Design

- **Date:** 2026-05-20
- **Status:** Approved (design); plan pending
- **Topic:** Add a `~/.moat/defaults.yaml` file (same schema as `moat.yaml`) whose contents merge into every project's loaded `Config`, so frequently-repeated declarations (e.g. `claude.bedrock.enabled: true`, `grants: [aws]`) can be set once per user.

## Motivation

Today every moat project needs a full `moat.yaml`, even when a user always wants the same agent, grants, and agent-specific block. agentbox solves this for itself via `~/.config/agentbox/config.toml`; moat's existing `~/.moat/config.yaml` (`internal/config/global.go` `GlobalConfig`) is too narrow — it covers proxy port, debug retention, and global mounts only. No path exists to set a `claude.bedrock` block (or any other `moat.yaml` field) globally.

The friction is real and recurring: an operator who always runs Claude-on-Bedrock has to repeat the same 5-line block in every project's `moat.yaml`. The fix is small, well-bounded, and reuses an existing merge precedent (the per-key Claude settings env merge shipped in the Bedrock work).

## Non-goals

- **Named per-user profiles** (`extends: <name>` / `--profile`). Out of scope for v1; single defaults file only. Can be added later if multi-mode use cases appear.
- **Per-agent-namespace defaults files** (`~/.moat/claude.defaults.yaml` etc.). One file covers all agents via the existing `claude:` / `codex:` / `gemini:` blocks in the shared schema.
- **A runtime `--no-defaults` flag**. Not needed if `moat config show` makes the merge transparent; deferred until real demand surfaces.
- **Tightening yaml parsing to reject unknown fields**. Matches today's lenient `yaml.v3` behavior; a separate change if wanted.

## Design

### 3.1 Defaults file: location and schema

A new optional file at `<GlobalConfigDir>/defaults.yaml` — by default `~/.moat/defaults.yaml`, or `$MOAT_HOME/defaults.yaml` when `MOAT_HOME` is set (existing convention, `internal/config/global.go:99-108`).

The file's YAML schema is **identical to `moat.yaml`** (the existing `Config` struct in `internal/config/config.go`). No new struct type. Same parser, same field names, same yaml tags. Any field that's valid in `moat.yaml` is valid in `defaults.yaml`.

Missing file is a silent no-op (no defaults applied). Empty file behaves as if every field were unset.

### 3.2 Loading & merge: `config.Load` flow

`internal/config/config.Load(dir)` is extended (additive — no signature change):

1. Parse the project's `moat.yaml` (today's behavior).
2. Load `defaults.yaml` via a new `LoadDefaults()` helper. Returns `(*Config, nil)` on success, `(nil, nil)` on missing file, `(nil, err)` on parse error.
3. Merge defaults into the project Config via a new `MergeConfig(defaults, project *Config) *Config` (project is the "override"; defaults the "base").
4. Run the existing validation on the **merged** result. The validation block at `config.go:~590-650` is unchanged.

The order is significant: defaults loaded first, project merged on top. The merge function below encodes "project value wins per field when set."

### 3.3 Merge rules

Per-field semantics — matches the precedent shipped for Claude `Settings.Env` (the per-key map merge from Task 4 of the Bedrock work):

| Field shape | Rule |
|---|---|
| Scalar (`string`, `int`, `bool`) | Project wins if non-zero. Defaults fill in the zero case. |
| Pointer to struct or scalar (`*BedrockConfig`, `*bool`) | Project wins if non-nil. If both non-nil, recurse into the pointed-to value with the same rules. |
| Map (`map[string]string`, `map[string]bool`, `map[string]MCPServerSpec` etc.) | Per-key merge. Project value wins per key. Defaults' keys survive when not overridden. |
| Slice of strings (`grants`, `dependencies`) | Union; dedupe by string equality. Defaults' entries first, then project's additions. |
| Slice of structs (`mounts`, `network.rules`) | Append + dedupe by a stable per-type key (see §3.4). |
| Nested struct (`ClaudeConfig`, `NetworkConfig`, etc.) | Recursive merge with the same rules. |

### 3.4 Slice-of-struct dedup keys

For each struct slice in `Config`, the merge function uses an explicit stable key (no reflection in the live path):

| Slice | Element type | Dedup key |
|---|---|---|
| `Mounts` | `MountEntry` | `(Source, Target)` pair — same source mounted at the same target is the same mount; mounting the same source at different targets is intentional |
| `Network.Rules` | `netrules.NetworkRuleEntry` | `(Host, Method, Path)` tuple — covers the existing rule identity |

When defaults and project both produce an entry for the same key, **project's entry replaces defaults' entry** (consistent with the scalar/map "project wins" semantics, applied at the element level).

If a future slice-of-struct field is added to `Config` without a dedup key declared, the merge function must error rather than silently union-by-position. The coverage test (§3.7) guards this.

### 3.5 Validation

Validation runs on the **merged** `Config`. Defaults in isolation may be "invalid" — for example `claude.bedrock.enabled: true` without `grants: [aws]` — as long as the project adds the missing piece (or its merged grants list contains `aws`). This is by design: defaults are partial and require project context to be valid.

The existing validation in `config.go:Load` is unchanged; it operates on whatever `Config` it receives. The merge function is pure and does not call validation.

### 3.6 `moat config show`

To prevent action-at-a-distance ("why is grant `aws` here? I never declared it") this feature ships with a small read-only sibling command:

```
moat config show                     # print merged config as YAML (cwd as project)
moat config show --source            # annotate each field with its origin
moat config show --workspace <path>  # inspect a non-cwd project
moat config show --no-defaults       # show project-only Config (useful for diffing)
```

(`--no-defaults` is included here because it's free once we already separate the two loads; not contradicting the v1 non-goal which referred to a runtime flag affecting `moat run`/`moat claude` behavior.)

`--source` output annotates each emitted field/element with `(defaults)`, `(project)`, or `(merged)`. Output format: standard YAML with end-of-line comments. Style precedent: `kubectl config view` / `git config --show-origin`.

Implementation: the merge function returns both the merged `Config` AND a parallel "source map" (`map[<field-path>]Source`). The CLI command renders the merged Config as YAML and post-processes each line to append `# from <source>` comments.

Out of scope for `moat config show`: `--diff` against defaults, `--set` / write-back, editing. Read-only inspection only.

### 3.7 Tests

- **Per-field merge tests** in `internal/config/merge_test.go`: one named subtest per top-level `Config` field, plus per-shape generic cases (scalar override, map per-key, slice union + dedupe, pointer-to-struct recurse, nested-struct recurse).
- **Coverage guard** in `merge_test.go`: a reflection-driven test (`TestMergeConfigCoversAllFields`) that walks the `Config` struct's exported fields and fails if any field name is missing from a manually-maintained "covered fields" list. New field added to `Config` without merge support → test failure, with a message pointing at the omitted field. Reflection is in **tests only**, not in the live merge path.
- **Round-trip tests** in `merge_test.go`: a fixture defaults.yaml + a fixture moat.yaml + a fixture expected-merged.yaml; the test loads the inputs, merges, and asserts deep-equality on the output. One fixture per "interesting" merge interaction (env precedence, grant union, bedrock pointer + scalar override).
- **`moat config show` tests** in `cmd/moat/cli/config_test.go`: table-driven against fixture inputs; assert YAML output exact for both `--source` on and off; assert `--no-defaults` ignores the defaults file.
- **Manual / out-of-CI**: set `~/.moat/defaults.yaml` to `agent: claude, grants: [aws], claude.bedrock.enabled: true`; create an empty project containing only `agent: claude`; run `moat claude` and confirm Bedrock-mode container.

### 3.8 Daemon-side considerations

None. Defaults loading is CLI-side; the merged Config flows downstream (to the daemon, to provider plumbing) exactly as today. Daemon API is untouched. Older daemons paired with newer CLIs see the same `Config` shape as today — they receive the resolved merged result, not the layered inputs.

## Files to change

| File | Change |
|---|---|
| `internal/config/defaults.go` (new) | `LoadDefaults() (*Config, error)` reads `<GlobalConfigDir>/defaults.yaml`; nil-and-nil on missing file. |
| `internal/config/merge.go` (new) | `MergeConfig(defaults, project *Config) (*Config, SourceMap)` — pure, hand-written per-field merge, no reflection in the live path. Returns the resolved Config and a parallel source map for `--source` annotation. |
| `internal/config/merge_test.go` (new) | Per-field merge tests; reflection-guarded coverage test; round-trip fixture tests. |
| `internal/config/config.go` | `Load(dir)` calls `LoadDefaults` and `MergeConfig` before validation. ~10-line change. |
| `internal/config/config_test.go` | One new test confirming defaults are loaded + merged via the public `Load` path. |
| `cmd/moat/cli/config.go` (new) | `moat config show` command (Cobra). Flags: `--source`, `--workspace`, `--no-defaults`. |
| `cmd/moat/cli/config_test.go` (new) | Output stability tests. |
| `docs/content/reference/02-moat-yaml.md` | New "Defaults" subsection: file location, merge rules, fully-worked example. |
| `docs/content/reference/01-cli.md` | `moat config show` documented in the CLI reference. |
| `CHANGELOG.md` | **Added** entry. |

## Risks

- **Action-at-a-distance.** A teammate opening someone else's project may see container behavior that doesn't match the visible `moat.yaml`. Mitigated by shipping `moat config show --source` as part of the feature (not deferred) and documenting the merge rules prominently in the moat.yaml reference.
- **Slice-of-struct merge ergonomics.** Mounts and network rules use a stable dedup key. If users want "defaults always provides mount X, project can disable X," that's not supported — defaults' entries cannot be removed by the project, only overridden by an entry with the same key. Acceptable for v1; document the rule. A future `delete:` directive could be added if real demand appears.
- **Merge-function staleness.** If `Config` gains a new field and the developer forgets to extend `MergeConfig`, the merge silently drops it. Mitigated by the reflection-guarded coverage test (§3.7).
- **`moat config show` output stability.** As a documented CLI command, its output format becomes part of the contract. Mitigated by output-stability tests against fixed inputs. Major output changes are reserved for a future flag (e.g. `--format json`).

## Future extensions (deferred, not blocked)

- Named profiles (`~/.moat/profiles/<name>.yaml` + `extends:` in moat.yaml or `--profile` flag) — can layer cleanly on top of this design without breaking the defaults-file path.
- `moat config edit` — opens the effective merged config or the defaults file in `$EDITOR`.
- `delete:` directive in defaults to allow defaults' entries to be removed by projects — only if real use cases appear.
