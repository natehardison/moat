# AWS Credential Pass-through (No-Assume Mode) — Design

- **Date:** 2026-05-19
- **Status:** Approved (design); plan pending
- **Topic:** Make moat's AWS grant work for environments that issue only role-scoped credentials and have no usable base identity for `sts:AssumeRole`.

## Goal

Let `moat grant aws` accept a configuration that says "this AWS shared-config
profile already yields the final credentials I need — don't call `AssumeRole`,
just serve them." Enable Claude-on-Bedrock (and any other moat AWS consumer)
in environments where the operator has no IAM user, no usable base identity,
and the only role they can target in the relevant account *is* the role itself.

## Non-goals

- Pass-through from the daemon-host **default credential chain** without an
  explicit profile (footgun: silently leaks ambient host creds into the
  container). The bare `moat grant aws` invocation remains an error.
- SigV4 re-signing in the proxy. Out of scope here; see the Claude-on-Bedrock
  design (`2026-05-19-claude-bedrock-design.md`) for the rationale.
- Storing/executing an arbitrary command line as the credential source. The
  user wires their broker into a shared-config profile's `credential_process`;
  moat resolves through the profile.
- Changes to how the container fetches credentials. The container-side helper
  and endpoint URL are unaffected — only the **host-side** behavior in the
  daemon changes.

## Background: why moat is `AssumeRole`-mandatory today

moat's AWS grant is built around an IAM-role identity:

- `cmd/moat/cli/grant.go:65` — `--aws-profile` is documented as
  *"AWS shared config profile **for role assumption**."*
- `internal/providers/aws/grant.go:69-87` — interactive prompt requires a role
  ARN; `ParseRoleARN` rejects anything that isn't `arn:aws:iam::…:role/…`.
- `internal/providers/aws/credential_provider.go:46-66` (the **live** serving
  path, instantiated at `manager.go:911`, `daemon/server.go:233`,
  `daemon/persist.go:232`) — applies `WithSharedConfigProfile(profile)` only
  to build the `awsCfg` used as the *source identity* for `sts:AssumeRole`,
  then `sts.NewFromConfig(awsCfg)` and `AssumeRole(roleARN)`.

This optimizes for the standing-keys threat model: when the host has
long-lived, broadly-scoped credentials (IAM user keys, broad SSO session),
the mandatory `AssumeRole` step constrains what the agent sees to a
purpose-built, short-lived, separately-revocable session with its own
`RoleSessionName=moat-<…>` audit discriminator.

It is a poor fit when the host has **only** role-scoped, ephemeral
credentials issued by an org broker (SSO/SAML/OIDC wrapper exposing
`credential_process`):

- There is no underlying base identity to call `AssumeRole` from.
- The only role available in the target account *is* the role you'd want to
  assume — i.e., self-assumption, typically trust-policy-forbidden and
  always a no-op for credential semantics.
- The credentials handed out by the broker are already short-lived and role-
  scoped, so the AssumeRole layer adds zero security and a hard prerequisite
  the operator cannot satisfy.

## Design

### 3.1 Grant UX

Make the role ARN **optional** in `moat grant aws`. The combination determines
the mode:

| Inputs | Behavior | Stored `source` |
|---|---|---|
| role ARN given (with or without `--aws-profile`) | `AssumeRole` (unchanged) | `role` |
| no role ARN, `--aws-profile <name>` given | Pass-through: serve the profile's resolved creds | `profile` |
| no role ARN, no `--aws-profile` | Hard error (footgun guard) | n/a |

`--aws-profile default` is the explicit escape hatch for "I really do mean
the host's default profile."

The error for the bare invocation is actionable:

> `moat grant aws` requires either an IAM role ARN to assume, or
> `--aws-profile <name>` for a profile whose credentials moat should serve
> directly. Run `moat grant aws --help` for examples.

### 3.2 Stored credential schema

`provider.Credential` gains one explicit metadata key:

```go
const MetaKeySource = "source" // "role" | "profile"
```

Today's grant — unchanged on the wire when re-created:

```go
Credential{
    Provider: "aws",
    Token:    "arn:aws:iam::123:role/X",
    Metadata: {"source": "role", "region": "..."},
}
```

New pass-through grant:

```go
Credential{
    Provider: "aws",
    Token:    "",
    Metadata: {"source": "profile", "profile": "corp-broker", "region": "..."},
}
```

Invariants enforced by `ConfigFromCredential`:

- `source = role` → `Token` (role ARN) non-empty, `ParseRoleARN` valid;
  `Metadata.profile` optional.
- `source = profile` → `Token` empty; `Metadata.profile` required, non-empty.
- Missing `source` key → treat as `role` for backward compatibility (existing
  stored credentials carry no `source`; they all behave as before).

### 3.3 Live serving path: branch in `CredentialProvider`

The branch lives in `internal/providers/aws/credential_provider.go`. Today's
`getCredentials` (paraphrased) always builds an `awsCfg` and then
`AssumeRole`s. After this change:

```go
switch p.source {
case "profile":
    return p.awsCfg.Credentials.Retrieve(ctx) // SDK CredentialsCache handles refresh
default: // "role"
    return p.stsClient.AssumeRole(ctx, p.assumeRoleInput())
}
```

`LoadDefaultConfig(WithSharedConfigProfile(profile))` already wraps
`Credentials` in a `CredentialsCache` that re-invokes the underlying
provider (including `credential_process`) on expiry, honoring the returned
`Expires`. moat's endpoint-level cache (`h.cached`/`h.expiration` and the
5-minute pre-expiry refresh buffer) is preserved, populated from
`Credentials.Expires` instead of `AssumeRoleOutput.Credentials.Expiration`.

### 3.4 Two-implementations reconciliation

There are two AWS credential serving types today:

- `awsprov.CredentialProvider` (`credential_provider.go`) — the **live** path
  used at runtime; honors `--aws-profile`.
- `awsprov.EndpointHandler` (`endpoint.go`) — registered by
  `provider.RegisterEndpoints` (`provider.go:69`) but ignores the profile
  entirely (default chain only); also has its own STS client wiring.

Today this inconsistency is latent — the live runtime callers use
`CredentialProvider`. Implementing pass-through in two places would
double the surface and risk drift.

**Decision:** the pass-through branch is added **only** to `CredentialProvider`.
During implementation we determine whether `EndpointHandler` has any live
caller; the plan's first task does a call-graph grep. If unused, delete
`EndpointHandler` and have `provider.RegisterEndpoints` construct a
`CredentialProvider` instead. If used, route `EndpointHandler.getCredentials`
through a `CredentialProvider` so both modes share one implementation.

Either way, the post-change repo has exactly one AWS credential-acquisition
path.

### 3.5 Grant-time validation

`grant.go`'s pre-save validation currently performs a live `sts:AssumeRole`
against the supplied role to fail fast on misconfiguration. After this change:

- **`source = role`** — unchanged: probe `AssumeRole`.
- **`source = profile`** —
  1. `LoadDefaultConfig(WithSharedConfigProfile(profile))` +
     `Credentials.Retrieve(ctx)` must return non-empty creds. Any error is
     surfaced verbatim (almost always actionable: profile missing,
     `credential_process` not on `PATH`, broker session expired).
  2. **Best-effort** `sts:GetCallerIdentity` for a confirmation echo line
     ("Bound to identity `arn:aws:sts::123:assumed-role/MyRole/session`").
     Failure is non-fatal — some environments block `GetCallerIdentity` via
     SCPs, and the grant still works for what the operator needs.
  3. No `AssumeRole` call.

### 3.6 Daemon API backward compatibility

Per `CLAUDE.md` "Proxy Daemon" rule, the daemon API is additive-only.

- New CLI sends a credential whose `Metadata` includes the optional `source`
  key. The serialized envelope shape is unchanged; only one new metadata
  string key is added.
- **Older daemon, new CLI:** receives `source = profile` with empty Token.
  `ConfigFromCredential` (in the older daemon) ignores the unknown
  `source` key; with empty Token the existing `ParseRoleARN`/AssumeRole
  path errors clearly with "invalid role ARN" / "cannot assume role" —
  safe failure, not silent misbehavior. The pre-existing daemon-version
  mismatch error path covers this gracefully.
- **Newer daemon, old credential** (no `source` key): defaults to
  `source = role`. Behavior identical to today, bit-for-bit.
- **Newer daemon, new credential:** branches on `source`.

No endpoints renamed/removed; no fields removed; one optional metadata key
added.

### 3.7 Bedrock interaction

The Claude-on-Bedrock feature (`2026-05-19-claude-bedrock-design.md`)
consumes moat's credential endpoint via the in-container helper. That
contract is unchanged — Bedrock works against either `source = role` or
`source = profile` transparently. This is the design's primary motivating
consumer but it requires no Bedrock-side code changes.

### 3.8 Security trade-offs (must be documented)

Pass-through changes the security posture in two specific, documented ways:

- **CloudTrail attribution.** API calls are attributed to whatever identity
  the profile yields (typically `assumed-role/<role>/<broker-session>`)
  with no `moat-<…>` `RoleSessionName` discriminator. Agent calls are
  indistinguishable from direct operator use of the same role. Operators
  who rely on per-run session-name attribution should stay on
  `source = role` if they have a usable base identity.
- **Revocation pathway.** Revocation moves from "rotate moat's
  AssumeRole trust" to "revoke at the upstream broker." This is the
  expected/only path in environments without a usable base identity; not
  worse for those environments, materially different for operators who
  previously relied on moat-controlled revocation.

The scope mechanism (the role itself) is unchanged: the container only
operates under the role the profile yields, which is already the
agent-scoped target role.

## Files to change

| File | Change |
|---|---|
| `cmd/moat/cli/grant.go` | Role ARN becomes optional when `--aws-profile` is given; bare invocation hard-errors with the actionable message in §3.1; `--help` updated. |
| `internal/providers/aws/grant.go` | `MetaKeySource = "source"`; mode-aware interactive flow; `Config` gains a `Source` field; mode-aware `ConfigFromCredential` with the §3.2 invariants; mode-aware pre-save validation (§3.5). |
| `internal/providers/aws/credential_provider.go` | `CredentialProvider` carries the resolved source; `getCredentials` branches per §3.3; profile mode uses `awsCfg.Credentials.Retrieve(ctx)` (SDK cache handles refresh). |
| `internal/providers/aws/endpoint.go` / `provider.go` | Reconcile per §3.4: either delete `EndpointHandler` and route `RegisterEndpoints` through `CredentialProvider`, or have `EndpointHandler` delegate to a `CredentialProvider`. Decision driven by call-graph in plan's first task. |
| `internal/providers/aws/doc.go` | Update the package-level doc to describe both `source` values and where each is selected. |
| `docs/content/reference/01-cli.md` | Document the new `moat grant aws --aws-profile <name>` (no role) form, the precedence rule, and the security trade-offs. |
| `docs/content/reference/04-grants.md` | Document the two AWS grant modes and the metadata schema. |
| `docs/content/guides/14-claude-bedrock.md` | Add a "no-AssumeRole environments" subsection pointing operators to this mode. |
| `CHANGELOG.md` | **Added** entry. |

## Testing

- **Unit (`grant_test.go`):** `ConfigFromCredential` round-trip for both
  sources; invariant violations (`source = profile` with `Token` set; `source
  = role` without `Token`; `source = profile` without `Metadata.profile`)
  produce specific errors; missing-`source` defaults to `role`.
- **Unit (`credential_provider_test.go`):** profile mode calls
  `Credentials.Retrieve()` and **does not** call `AssumeRole` (assert via a
  fake `STSAssumeRoler` whose AssumeRole method fails the test if invoked);
  role mode unchanged.
- **Unit (whichever survives reconciliation):** if `EndpointHandler` is kept,
  parity test that it produces the same JSON for both modes as
  `CredentialProvider`.
- **CLI integration (`cmd/moat/cli` test):** `moat grant aws --aws-profile
  foo` (no role) is accepted and stores `source = profile`; `moat grant aws`
  bare errors with the documented message; `moat grant aws <roleARN>`
  unchanged.
- **Manual / out-of-CI:** end-to-end against a real `credential_process`
  profile (cannot be hermetic). Documented in the guide.

## Risks

- **Operators conflating "no-assume" with "no-scope."** The scope is still
  the role the profile yields; that needs to be clear in the docs (§3.8).
- **Profile `credential_process` requires `PATH`/env in the daemon's
  environment.** The daemon inherits the user's environment at start; if
  `credential_process` references a command that isn't on the daemon's
  `PATH`, `Retrieve()` fails. Grant-time validation (§3.5 step 1) catches
  this immediately with the broker tool's actual error message.
- **`GetCallerIdentity` may be blocked** by some org SCPs even though
  Bedrock works. Treated as non-fatal at grant time (§3.5 step 2); echo
  line falls back to "Bound to profile `<name>` (identity unknown — STS
  GetCallerIdentity denied)" or similar.
