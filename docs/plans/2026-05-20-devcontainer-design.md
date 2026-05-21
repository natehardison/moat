# Devcontainer support for moat

**Status:** Design — awaiting review
**Date:** 2026-05-20

## Summary

Add `.devcontainer/devcontainer.json` detection and construction to moat,
mirroring the model agentbox uses. When a workspace ships a devcontainer
and moat.yaml does not declare its own image, moat builds the
devcontainer's base image, layers moat's existing overlay on top, and
honors the devcontainer's user, workspace folder, env, mounts, and
lifecycle hooks.

## Goals

- Drop a `.devcontainer/devcontainer.json` in a repo and `moat run` it,
  with no other config needed beyond `--agent` and `--grant`.
- Honor enough of the devcontainer spec that a VS Code-compatible
  `.devcontainer/` keeps working under moat without edits.
- Preserve moat's image layering: agent CLI, proxy CA, init scripts,
  grant infra still apply on top of any devcontainer base.
- Keep moat.yaml authoritative when both files specify the image — no
  silent merging of two sources of truth.

## Non-goals

- `features` (devcontainer features registry / OCI install protocol).
- `runArgs` (arbitrary docker run flags pass-through).
- `customizations.*` (IDE-specific hints).
- `forwardPorts` / `portsAttributes` — moat has its own `ports:`.
- Dotfiles repo cloning.
- `dockerComposeFile` — moat's `services:` covers multi-container.
- Compatibility with Microsoft's `devcontainer` CLI.

## Detection and precedence

Moat detects a devcontainer by the existence of
`<workspace>/.devcontainer/devcontainer.json`. `devcontainer.Detect`
returns the parsed config, `nil` if absent, or an error if the file
exists but won't parse (hard fail; we never silently ignore a broken
devcontainer.json).

Decision in `internal/run/manager.go`, after `config.Load` but before
`ImageSpec` construction:

```
useDevcontainer := dcCfg != nil &&
    (moatCfg == nil ||
     (moatCfg.BaseImage == "" && len(moatCfg.Dependencies) == 0))
```

Behaviors:

| moat.yaml | devcontainer | Result |
| --- | --- | --- |
| absent | absent | unchanged error path |
| present, no `base_image`/`deps` | absent | unchanged behavior |
| present, no `base_image`/`deps` | present | devcontainer drives image, user, workspaceFolder, env, hooks, mounts; moat.yaml drives agent, grants, network, mcp, services, etc. |
| present, has `base_image:` or `dependencies:` | present | devcontainer ignored for image; warn once at run; moat.yaml unchanged |
| absent | present | devcontainer drives everything image-side; agent/grants from CLI flags |

## Package layout

New package at `internal/devcontainer/`:

- `config.go` — `Config`, `Mount`, `BuildConfig` types; `Detect`,
  `parseJSONC`, `expandVars`.
- `build.go` — `BuildBase`, `EnvOverlay`. Owns the Stage A cache.
- `hooks.go` — `RunHook` (in-container), `RunInitializeCommand` (host).
- `probe.go` — `ProbeUserEnv` for the user's login-shell env.
- `testdata/` — fixture devcontainer.json files for unit tests.

Exported `Config`:

```go
type Config struct {
    Image           string
    Build           *BuildConfig      // dockerfile, context, args, target
    User            string            // remoteUser ?? containerUser ?? "root"
    Home            string            // /root or /home/<user>
    WorkspaceFolder string            // defaults to /workspaces/<basename>
    ContainerEnv    map[string]string // baked into image
    RemoteEnv       map[string]string // injected at exec time
    Mounts          []Mount
    InitializeCmd   string
    OnCreateCmd     string
    PostCreateCmd   string
    PostStartCmd    string
    SourcePath      string            // absolute path to devcontainer.json
    UpdateRemoteUserUID bool          // honored as the UID-remap on/off
}
```

`internal/run/manager.go` integration: one new helper
`prepareDevcontainerBase(ctx, opts, dc)` returning the base image tag,
lifecycle hooks, extra mounts, extra env, user, home, workdir. The
existing image-resolve/build code reads the base tag into
`ImageSpec.BaseImage`. Lifecycle hooks and probed env are stashed on
the `Run` struct so subsequent `Start` paths can re-run
`postStartCommand`.

## Parsing

**JSONC stripping.** Single-pass state machine over the bytes, tracks
`in_string` and `escaped`, elides `// … \n` and `/* … */` comments
outside strings. Trailing commas in objects/arrays are also stripped
(VS Code tolerates them, real-world files use them).

**Variable expansion.** Resolved against the workspace path and the
already-resolved `containerEnv` map:

| Reference | Value |
| --- | --- |
| `${localWorkspaceFolder}` | absolute host workspace path |
| `${localWorkspaceFolderBasename}` | basename of host workspace |
| `${containerWorkspaceFolder}` | resolved `workspaceFolder`, defaulting to `/workspaces/<basename>` |
| `${containerWorkspaceFolderBasename}` | basename of resolved workspace folder |
| `${localEnv:NAME}` / `${localEnv:NAME:default}` | host env var, with optional default |
| `${containerEnv:NAME}` / `${containerEnv:NAME:default}` | only inside `remoteEnv`; resolved against already-expanded `containerEnv` |
| anything else | left literal, debug-logged |

Two-pass for env: resolve `containerEnv` (can reference local vars),
then `remoteEnv` (can reference both).

**Lifecycle command shapes.** Spec allows string, array, or object:

- string → used verbatim, executed via `/bin/sh -c`
- array → first element is the program, rest are argv (no shell)
- object → values run sequentially, joined with `&&` (the spec's
  "parallel" semantics is intentionally serialized to avoid implicit
  concurrency surprises; matches agentbox)

**Mounts.** Two input forms normalize to:

```go
type Mount struct {
    Source   string
    Target   string
    Type     string // "bind" or "volume"
    ReadOnly bool
}
```

Translates 1:1 to moat's `container.MountConfig`. Unknown `type` is a
hard error.

**User / home.** `remoteUser` wins over `containerUser`. Default
`root`. Home is `/root` for root, `/home/<user>` otherwise. We don't
probe `/etc/passwd` at parse time — the probe runs later in the
container.

**Validation:**

Hard fail (error from `Detect`):
- Missing both `image:` and `build.dockerfile:`
- Malformed JSON or unclosed string
- Unsupported mount `type`

Warn-and-drop (single `ui.Warn` listing them):
- `features`, `runArgs`, `forwardPorts`, `portsAttributes`,
  `customizations`, `hostRequirements`, `userEnvProbe`, anything else
  outside our subset

`updateRemoteUserUID` (default true) is honored — it's the on/off
switch for UID remapping. Not in the warn list.

## Two-stage image build

**Stage A — devcontainer base.** Produced by `devcontainer.BuildBase`:

1. If `dc.InitializeCmd != ""`, run it on the host, `cwd=workspace`,
   inheriting host env. Non-zero exit → hard fail before any container
   work.
2. `baseHash = sha256("DevcontainerBase" || sorted(rel-path || file-bytes
   for every file under .devcontainer/))`. Identical configs at
   different paths share the cached image.
3. `baseTag = "moat-devcontainer-<workspace-basename>:base-<baseHash[:12]>"`.
4. If `runtime.ImageExists(baseTag)` and `--rebuild` not set, skip.
   Otherwise:
   - `image:` set → `docker pull <image>`, then `docker tag <image> <baseTag>`
   - `build:` set → `docker build -t <baseTag> -f <dockerfile> [--target X]
     [--build-arg K=V ...] <context>`, with `dockerfile` and `context`
     resolved relative to `.devcontainer/`
5. If `dc.ContainerEnv` non-empty, write a tiny overlay Dockerfile
   (`FROM <baseTag>` + one `ENV` per entry) and build it as `baseTag`
   (replaces the tag). The base-build cache survives env tweaks because
   env doesn't go through `--build-arg`.

**Stage B — moat overlay.** The existing pipeline in `manager.go`
(Dockerfile generated by `internal/deps`) runs unchanged except that
`ImageSpec.BaseImage = baseTag`. The existing image-tag function
already content-addresses on `ImageSpec.BaseImage`, so the overlay tag
auto-invalidates when the base changes.

Stage B always runs whenever moat would normally build a custom image
(any agent, any grant needing init, any dep, any other `ImageSpec`
trigger). In practice every real moat run has an agent, so Stage B is
effectively mandatory. If a devcontainer base already has the agent
CLI installed, Stage B re-installs/overwrites it — the moat-managed
version wins for reproducibility.

**Caching directories.** Build contexts (when `build.context`
references workspace files) are passed straight to the runtime. The
image tag itself is the cache key; we trust the container runtime's
layer cache for everything else.

**Runtime compatibility.** Apple containers and Docker both support
`docker build -f`. Some Apple-containers limitations may surface (e.g.,
BuildKit syntax in user Dockerfiles) — we don't try to detect or
rewrite those; we let the runtime fail with its native error.

**`--rebuild`** removes both `baseTag` and the overlay tag and re-runs
`initializeCommand`. Per-stage `--no-cache` is not exposed; rebuild
means rebuild.

## Workspace folder, remoteUser, UID remapping

**Workspace mount target.** When a devcontainer is active, the
workspace mounts at `dc.WorkspaceFolder` (defaulting to
`/workspaces/<basename>`) instead of `/workspace`. The existing
"skip if explicit mount to /workspace" logic in `manager.go` is
generalized: skip the implicit workspace mount if any explicit mount
targets the resolved workspace folder. `Run.Workdir` becomes
`dc.WorkspaceFolder` for devcontainer runs.

**Exec user.** `Run.User` becomes `dc.User`. All moat-issued
`docker exec` / `containers-cli exec` calls — pre_run hooks, agent
launch, `moat exec` — use `-u <dc.User>` and inject
`USER=<dc.User>`, `HOME=<dc.Home>`. Existing root-required steps
(firewall setup, etc.) keep using `-u root` explicitly. The probed user
env from `probe.go` is merged in for hook execution.

**UID/GID remapping on Linux.** Appended as the final layer of the
Stage B Dockerfile (still effectively two `docker build` invocations
total):

```dockerfile
ARG MOAT_USER
ARG MOAT_UID
ARG MOAT_GID
RUN if [ "$MOAT_USER" != "root" ] && id "$MOAT_USER" >/dev/null 2>&1; then \
      groupmod -o -g "$MOAT_GID" "$(id -gn "$MOAT_USER")" && \
      usermod  -o -u "$MOAT_UID" "$MOAT_USER" && \
      chown -R "$MOAT_UID:$MOAT_GID" "$(getent passwd "$MOAT_USER" | cut -d: -f6)" 2>/dev/null || true; \
    fi
```

- `MOAT_UID`/`MOAT_GID` come from `stat(workspace)` on Linux (moat
  already has `getWorkspaceOwner`).
- `-o` allows non-unique IDs.
- `chown` is best-effort and only fixes the user's home dir; things
  baked outside the home dir stay at the original UID. Matches what
  `updateRemoteUserUID` does in VS Code.
- On macOS (Docker Desktop / Apple containers) this stage is skipped.
- If `dc.User == "root"`, this stage is skipped.
- If `dc.UpdateRemoteUserUID == false`, this stage is skipped.

**Caching with UID/GID baked in.** New fields on `ImageSpec`:
`RemapUser`, `RemapUID`, `RemapGID`. The overlay tag hash incorporates
them so two developers with different host UIDs on the same workspace
get different overlay images but share the Stage A base.

## Lifecycle hook composition

**Fresh `moat run` ordering:**

| # | Hook | Where | Source |
| - | ---- | ----- | ------ |
| 1 | `initializeCommand` | host, cwd=workspace | devcontainer |
| 2 | Stage A build | — | — |
| 3 | Stage B build | — | — |
| 4 | `hooks.post_build_root` | Stage B Dockerfile, as root | moat.yaml |
| 5 | `hooks.post_build` | Stage B Dockerfile, as moat user | moat.yaml |
| 6 | container create + UID remap baked from Stage B | — | — |
| 7 | `onCreateCommand` | in-container, as `remoteUser`, probed env | devcontainer |
| 8 | `postCreateCommand` | in-container, as `remoteUser`, probed env | devcontainer |
| 9 | `postStartCommand` | in-container, as `remoteUser`, probed env | devcontainer |
| 10 | `hooks.pre_run` | in-container, as `remoteUser` | moat.yaml |
| 11 | agent process | in-container | — |

**Reusing an existing container or `moat start` after stop:**

| # | Hook |
| - | ---- |
| 1 | `initializeCommand` (always — every run, per spec) |
| 2 | (no Stage A/B if cached) |
| 3 | (no onCreate/postCreate — container already exists) |
| 4 | `postStartCommand` |
| 5 | `hooks.pre_run` |
| 6 | agent process |

**Failure semantics:**
- `initializeCommand` non-zero → abort run before any container work.
- Stage A/B build errors → abort, surface the build log.
- `onCreateCommand` / `postCreateCommand` non-zero → abort. These are
  one-shot setup; if they fail the container is broken.
- `postStartCommand` non-zero → warn, continue (runs every start).
- `hooks.post_build*` failure → existing moat behavior.
- `hooks.pre_run` failure → existing moat behavior.

**Container-level mounts.** `dc.Mounts` append to the moat mounts list
after the implicit workspace mount and after any explicit `mounts:`
from moat.yaml. If a devcontainer mount targets a path already
occupied by an earlier mount, the later one wins (devcontainer
overrides moat default), with a warn log.

**Env composition** (later overrides earlier):

1. Image-baked env (from `containerEnv` overlay)
2. Moat's runtime injection (`MOAT_*`, proxy settings, etc.)
3. `dc.RemoteEnv` (after `${var}` expansion)
4. `moat.yaml` `env:`
5. `moat.yaml` `secrets:` (highest)

`containerEnv` is at the bottom because it's defaults baked into the
image; `remoteEnv` is higher because the user wrote it explicitly per
session. Moat.yaml is the user's project-level intent and should be
able to override anything the devcontainer hardcoded. Secrets always
win.

## CLI and UX changes

**`moat run`** — devcontainer is detected automatically. New flag
`--no-devcontainer` force-disables detection (mirrors VS Code's
"Reopen Folder Locally"). `--rebuild` is extended to invalidate the
Stage A base tag in addition to the Stage B overlay.

**`moat doctor`** — when run in a workspace with a devcontainer, adds a
"Devcontainer" section reporting parsed image source, user,
workspaceFolder, and which moat.yaml fields override which (or
"none — devcontainer drives image"). Surfaces problems before
`moat run` time.

**`moat init`** — detects an existing `.devcontainer/devcontainer.json`
and writes a minimal moat.yaml with only `agent:` and `grants:`, no
`base_image:`, no `dependencies:`. Top-of-file comment notes the
devcontainer is the image source of truth. If no devcontainer exists,
behavior is unchanged.

**`moat status`** — adds a `DevcontainerHash` field to the `Run` struct
(frozen at create time). `moat status <run>` shows whether the
workspace's current `.devcontainer/` has drifted, with a hint like
"devcontainer.json changed; `moat run --rebuild` to apply." Purely
informational.

**Errors:**
- Devcontainer parse error: print the file, line, JSON error, and a
  pointer to the supported-subset doc.
- `initializeCommand` failed: print command, exit code, tail of output.
- Stage A build failed: surface the runtime's native error; suggest
  `--rebuild` and `--no-devcontainer`.
- `image:` referenced an image that can't be pulled: "devcontainer.json
  declared `image: foo`; could not pull. Check the image name or your
  registry credentials."

**Docs to update:**
- `docs/content/reference/01-cli.md` — `--no-devcontainer`, `--rebuild`
  behavior change.
- `docs/content/reference/02-moat-yaml.md` — note that `base_image:`
  and `dependencies:` override an existing devcontainer.
- New `docs/content/guides/devcontainer.md` — walkthrough.
- `docs/content/concepts/` — short note in the existing image-selection
  concept page.
- `CHANGELOG.md` — `Added: devcontainer.json support` entry.

## Testing

**Unit tests:**

- `config_test.go` — table-driven against fixture devcontainer.json
  files in `internal/devcontainer/testdata/`: minimal `image:`, minimal
  `build:`, both `containerUser` and `remoteUser`, all four lifecycle
  command shapes, string and object mount forms, every `${var}`
  reference (including nested `${containerEnv:FOO}` inside
  `remoteEnv`), JSONC with comments and trailing commas,
  unsupported-field warn paths.
- `build_test.go` — fake runtime recording `docker` invocations:
  `BuildBase` produces correct `pull → tag` sequence for `image:`,
  correct `build -f -t --target --build-arg` sequence for `build:`,
  `initializeCommand` runs first, the `containerEnv` overlay
  Dockerfile is correctly formed, content hash is path-independent.
- `hooks_test.go` — fake runtime asserts `exec -u <user> -w <workspace>
  -e USER=... -e HOME=... -e PATH=...` args; hard-fail on
  `onCreate`/`postCreate`, warn on `postStart`.
- `probe_test.go` — fake runtime returning canned stdout from
  `/proc/self/environ` and `printenv`; assert UUID-delimited
  extraction, fallback path, `PATH` dedup, dropping
  `PWD`/`SHLVL`/`_`.

**Integration tests in `internal/run/`:**

- Extend `manager_test.go` with: (a) `.devcontainer/` and no moat.yaml,
  (b) moat.yaml silent on image + devcontainer present (devcontainer
  wins), (c) moat.yaml with `base_image:` + devcontainer present
  (warn + devcontainer ignored), (d) moat.yaml with `dependencies:` +
  devcontainer present (same). Fake runtime; assert
  `ImageSpec.BaseImage` is set correctly in each case.
- New `devcontainer_integration_test.go` — covers Stage A → Stage B
  handoff with both devcontainer hooks and moat.yaml hooks; asserts
  the order from the lifecycle table.

**E2E tests** (`internal/e2e/`, `-tags=e2e`, real runtime):

Three new scenarios under `internal/e2e/testdata/devcontainer/`:

1. `image-only`: devcontainer.json with
   `image: mcr.microsoft.com/devcontainers/base:bookworm`,
   `remoteUser: vscode`, no hooks. Assert container runs as `vscode`,
   workspace mounted at `/workspaces/<name>`, UID/GID match host
   workspace owner on Linux.
2. `dockerfile-build`: devcontainer.json with
   `build.dockerfile: Dockerfile` that adds a custom binary. Assert
   the binary is on PATH inside the container.
3. `full-lifecycle`: devcontainer.json exercising all four hooks. Each
   hook writes a marker file with the expected user/cwd/env, then a
   single assertion verifies marker files in the right order.

E2E is the only place we actually exercise UID remapping end-to-end;
everything below it can use fakes.

## Rollout

Stack of 3 PRs (plus an optional PR 0):

- **PR 0 (optional):** small refactor of `manager.go` extracting a
  `resolveBaseImage(ctx, opts, moatCfg, dc) (string, error)` helper,
  since the current code reads `opts.Config.BaseImage` inline. Tightens
  the PR 2 diff.
- **PR 1:** `internal/devcontainer/` package (parser, builder, hooks,
  probe) + unit tests. Dead code; not wired into the CLI flow yet.
  Reviewable in isolation.
- **PR 2:** Manager integration — Stage A/B wiring in
  `internal/run/manager.go`, precedence logic, `ImageSpec.BaseImage`
  plumbing, UID-remap Dockerfile fold-in, `Run.User`/`Run.Workdir`
  adjustments, `DevcontainerHash` on `Run`. Includes integration tests
  and the `image-only` e2e. Devcontainer-driven flow now actually
  works.
- **PR 3:** CLI + docs polish — `--no-devcontainer`, `moat init`
  detection, `moat doctor` section, `moat status` drift hint, error
  message improvements, remaining e2e scenarios, `CHANGELOG.md`,
  reference and guide docs.

## Compatibility

Nothing existing breaks. Precedence rules ensure devcontainer never
kicks in for an existing project that has `base_image:` or
`dependencies:` set. Projects with both — and that didn't realize
devcontainer was being ignored — get a one-time warning. No data
migration, no daemon-API change (devcontainer is purely CLI/manager
side).
