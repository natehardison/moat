---
title: "Using devcontainer.json"
description: "Run agents in workspaces that declare a .devcontainer/devcontainer.json."
keywords: ["moat", "devcontainer", "dev containers", "docker", "image", "lifecycle hooks"]
---

# Using devcontainer.json

If your workspace contains a `.devcontainer/devcontainer.json`, moat uses it as the image source.

## Precedence

| moat.yaml | `.devcontainer/` | What moat uses for the image |
|-----------|------------------|------------------------------|
| absent or silent on image | absent | default image |
| absent or silent on image | present | devcontainer.json |
| sets `base_image:` or `dependencies:` | absent | moat.yaml |
| sets `base_image:` or `dependencies:` | present | moat.yaml (devcontainer ignored, warned) |

The devcontainer-driven flow runs without a `moat.yaml`; agent and grants come from CLI flags:

```bash
moat run --agent claude --grant github
```

## Supported devcontainer.json fields

| Field | Behavior |
|-------|----------|
| `image` | Pulled and tagged as the Stage A base. |
| `build.dockerfile` (+ `build.context`, `build.args`, `build.target`) | Built as the Stage A base. |
| `remoteUser` / `containerUser` | Container exec user (`remoteUser` wins). |
| `workspaceFolder` | Workspace mount target (default `/workspaces/<basename>`). |
| `containerEnv` | Baked into the image as `ENV` instructions. |
| `remoteEnv` | Injected at container exec time. |
| `mounts` | Bind and volume mounts. Unsupported types are a parse error. |
| `initializeCommand` | Runs on the host before image build. |
| `onCreateCommand` | Runs in the container once on creation. |
| `postCreateCommand` | Runs in the container once on creation. |
| `postStartCommand` | Runs in the container on every start. |
| `updateRemoteUserUID` | UID remap on/off switch (default `true`). |

Other devcontainer.json fields (`features`, `runArgs`, `customizations`, `forwardPorts`, etc.) are not supported. Moat prints a warning listing the ignored fields at parse time.

## How moat builds the image

Two-stage build:

1. **Stage A** — devcontainer base. Tag: `moat-devcontainer-<workspace-basename>:base-<sha[:12]>`. Content-addressed on the files under `.devcontainer/`.
2. **Stage B** — moat overlay. Adds the agent CLI, proxy CA cert, grants infrastructure, and (on Linux) a UID remap so workspace files have correct ownership.

Both stages cache. `moat run --rebuild` invalidates both.

## Lifecycle hook order

On first `moat run`:

1. `initializeCommand` (host)
2. Stage A build
3. Stage B build
4. Container creation
5. `onCreateCommand` (container)
6. `postCreateCommand` (container)
7. `postStartCommand` (container)
8. `hooks.pre_run` from `moat.yaml` (container)
9. Agent process

On subsequent starts (after stop):

1. `initializeCommand` (host, every run)
2. `postStartCommand` (container)
3. `hooks.pre_run` (container)
4. Agent process

`onCreateCommand` and `postCreateCommand` are one-shot: they run once on first creation and are not re-run on restart.

`onCreateCommand` and `postCreateCommand` failures abort the run. `postStartCommand` failures print a warning and continue.

## UID/GID remapping on Linux

On Linux, moat remaps the devcontainer's `remoteUser` UID/GID to match the host workspace owner at image build time. This keeps files written inside the container owned correctly on the host.

Set `"updateRemoteUserUID": false` in `devcontainer.json` to disable.

On macOS (Docker Desktop or Apple containers), no remapping is needed and this step is skipped.

## Bypassing devcontainer detection

```bash
moat run --no-devcontainer
```

Treats the workspace as if no `devcontainer.json` is present.

## Drift detection

`moat status` compares the current `.devcontainer/` content hash against the hash stored when the run was created. If they differ, status prints:

```
hint: devcontainer.json changed for "<name>"; run `moat run --rebuild` to apply
```

The hint is informational; moat does not auto-rebuild on drift.

## Design

See [docs/plans/2026-05-20-devcontainer-design.md](https://github.com/majorcontext/moat/blob/main/docs/plans/2026-05-20-devcontainer-design.md) for the full design.
