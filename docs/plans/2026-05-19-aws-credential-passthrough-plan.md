# AWS Credential Pass-through (No-Assume Mode) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `moat grant aws` accept `--aws-profile <name>` *without* a role ARN, in which case moat serves that profile's resolved credentials directly to the container (no `sts:AssumeRole`).

**Architecture:** Additive. A new `source` metadata key on the AWS credential (`role` default, `profile` new) discriminates two acquisition paths in one unified `CredentialProvider`. Role mode is bit-for-bit today's behavior; profile mode resolves the named profile via the AWS SDK (which natively runs its `credential_process`) and serves `Credentials.Retrieve()` directly. Bare `moat grant aws` (no role, no profile) hard-errors as a footgun guard. Two-implementations reconciliation collapses `EndpointHandler` into `CredentialProvider` so there is exactly one acquisition path post-change.

**Tech Stack:** Go, AWS SDK for Go v2 (`config`, `aws`, `service/sts`), the repo's existing table-driven test style.

**Spec:** `docs/plans/2026-05-19-aws-credential-passthrough-design.md` (read it before starting).

**Conventions (from CLAUDE.md):** Conventional Commits, NO `Co-Authored-By` lines. Run `go build ./...` and the touched-package tests before each commit; `make lint` clean before merge.

---

## File Structure

| File | Responsibility | Action |
|---|---|---|
| `internal/providers/aws/grant.go` | `MetaKeySource` const; `Config.Source`; mode-aware `grant()`; mode-aware pre-save validation; `ConfigFromCredential` enforces the source invariants and defaults missing key to `role` | Modify |
| `internal/providers/aws/grant_test.go` | Round-trip + invariant tests | Modify (or create if absent) |
| `internal/providers/aws/credential_provider.go` | `CredentialProviderConfig.Source`; `CredentialProvider` stores both an STS client (role) and an `aws.CredentialsProvider` (profile); `GetCredentials` branches on source | Modify |
| `internal/providers/aws/credential_provider_test.go` | Tests for the new branch (profile-mode does not call AssumeRole) | Modify |
| `internal/providers/aws/provider.go` | Reconciliation per Task 5 (likely delete `RegisterEndpoints` or route it through `CredentialProvider`) | Modify |
| `internal/providers/aws/endpoint.go` / `endpoint_test.go` | Reconciliation per Task 5 | Modify or delete |
| `internal/providers/aws/doc.go` | Doc the two source modes | Modify |
| `cmd/moat/cli/grant.go` | `--role` becomes optional when `--aws-profile` given; bare AWS grant errors with actionable message; updated help text | Modify |
| `docs/content/reference/01-cli.md` | Document the new accepted form, the precedence rule, and security trade-offs | Modify |
| `docs/content/reference/04-grants.md` | Document both AWS source modes and the metadata schema | Modify |
| `docs/content/guides/14-claude-bedrock.md` | Cross-link from "no-AssumeRole environments" | Modify |
| `CHANGELOG.md` | **Added** entry | Modify |

---

## Task 1: `MetaKeySource` constant + `Config.Source` + `ConfigFromCredential` invariants

**Files:**
- Modify: `internal/providers/aws/grant.go` (`Metadata keys` block ~lines 19-25; `Config` struct ~lines 56-63; `ConfigFromCredential` ~lines 233-281)
- Test: `internal/providers/aws/grant_test.go` (file may exist; if not, create with `package aws`)

This task is data-layer only — adds the field, the constant, and the invariant-checking. No behavior change yet (no new modes used anywhere).

- [ ] **Step 1: Write the failing test**

Add (or append) to `internal/providers/aws/grant_test.go`:

```go
package aws

import (
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/provider"
)

func TestConfigFromCredentialSource(t *testing.T) {
	cases := []struct {
		name     string
		cred     *provider.Credential
		wantErr  string
		wantSrc  string
		wantRole string
		wantProf string
	}{
		{
			name: "missing source defaults to role",
			cred: &provider.Credential{
				Provider: "aws",
				Token:    "arn:aws:iam::123456789012:role/X",
				Metadata: map[string]string{"region": "us-west-2"},
			},
			wantSrc:  "role",
			wantRole: "arn:aws:iam::123456789012:role/X",
		},
		{
			name: "explicit source=role",
			cred: &provider.Credential{
				Provider: "aws",
				Token:    "arn:aws:iam::123456789012:role/X",
				Metadata: map[string]string{"source": "role", "region": "us-west-2"},
			},
			wantSrc:  "role",
			wantRole: "arn:aws:iam::123456789012:role/X",
		},
		{
			name: "source=profile",
			cred: &provider.Credential{
				Provider: "aws",
				Token:    "",
				Metadata: map[string]string{"source": "profile", "profile": "corp-broker", "region": "us-west-2"},
			},
			wantSrc:  "profile",
			wantProf: "corp-broker",
		},
		{
			name: "source=profile rejects non-empty Token",
			cred: &provider.Credential{
				Provider: "aws",
				Token:    "arn:aws:iam::123456789012:role/X",
				Metadata: map[string]string{"source": "profile", "profile": "corp-broker"},
			},
			wantErr: "source=profile must not carry a role ARN",
		},
		{
			name: "source=profile requires profile metadata",
			cred: &provider.Credential{
				Provider: "aws",
				Token:    "",
				Metadata: map[string]string{"source": "profile"},
			},
			wantErr: "source=profile requires the \"profile\" metadata key",
		},
		{
			name: "source=role requires Token",
			cred: &provider.Credential{
				Provider: "aws",
				Token:    "",
				Metadata: map[string]string{"source": "role"},
			},
			wantErr: "source=role requires a role ARN",
		},
		{
			name: "unknown source value rejected",
			cred: &provider.Credential{
				Provider: "aws",
				Token:    "arn:aws:iam::123456789012:role/X",
				Metadata: map[string]string{"source": "bogus"},
			},
			wantErr: "unknown source \"bogus\"",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ConfigFromCredential(tc.cred)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want substring %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.Source != tc.wantSrc {
				t.Errorf("Source = %q, want %q", cfg.Source, tc.wantSrc)
			}
			if cfg.RoleARN != tc.wantRole {
				t.Errorf("RoleARN = %q, want %q", cfg.RoleARN, tc.wantRole)
			}
			if cfg.Profile != tc.wantProf {
				t.Errorf("Profile = %q, want %q", cfg.Profile, tc.wantProf)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/providers/aws/ -run TestConfigFromCredentialSource -v`
Expected: FAIL — `cfg.Source` undefined (compile error).

- [ ] **Step 3: Add the constant, field, and validation**

In `internal/providers/aws/grant.go`, in the `Metadata keys for AWS credentials` const block (currently `MetaKeyRegion`/`MetaKeySessionDuration`/`MetaKeyExternalID`/`MetaKeyProfile`), add:

```go
	// MetaKeySource selects the credential acquisition path:
	//   "role"    (default): moat calls sts:AssumeRole on the stored RoleARN.
	//   "profile" (new):     moat serves the named profile's resolved creds directly.
	MetaKeySource = "source"
```

In the `Config` struct, add a `Source` field (after `Profile`):

```go
	// Source is the credential acquisition mode: "role" (default, AssumeRole)
	// or "profile" (serve the named profile's creds directly, no AssumeRole).
	Source string
```

In `ConfigFromCredential`, after the existing Metadata read block and BEFORE the legacy Scopes fallback, insert source resolution and validation:

```go
	// Resolve source mode (defaults to "role" for backward compatibility with
	// credentials stored before this field existed).
	if cred.Metadata != nil {
		if s := cred.Metadata[MetaKeySource]; s != "" {
			cfg.Source = s
		}
	}
	if cfg.Source == "" {
		cfg.Source = "role"
	}

	switch cfg.Source {
	case "role":
		if cfg.RoleARN == "" {
			return nil, fmt.Errorf("source=role requires a role ARN (credential Token is empty)")
		}
	case "profile":
		if cfg.RoleARN != "" {
			return nil, fmt.Errorf("source=profile must not carry a role ARN (credential Token must be empty)")
		}
		if cfg.Profile == "" {
			return nil, fmt.Errorf("source=profile requires the \"profile\" metadata key")
		}
	default:
		return nil, fmt.Errorf("unknown source %q (expected \"role\" or \"profile\")", cfg.Source)
	}
```

(`fmt` is already imported in `grant.go`.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/providers/aws/ -run TestConfigFromCredentialSource -v`
Expected: PASS (all 7 subtests).

Then full package: `go test ./internal/providers/aws/`
Expected: ok — existing tests must still pass (the missing-`source` default to `"role"` preserves their assumptions).

- [ ] **Step 5: Commit**

```bash
git add internal/providers/aws/grant.go internal/providers/aws/grant_test.go
git commit -m "feat(aws): add source metadata key with role/profile invariants"
```

---

## Task 2: Mode-aware `grant()` + pre-save validation

**Files:**
- Modify: `internal/providers/aws/grant.go` (`grant()` ~lines 69-167; `testAssumeRole` ~lines 201-230; add a sibling `testProfileRetrieve` helper)
- Test: `internal/providers/aws/grant_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/providers/aws/grant_test.go`:

```go
import "context"

func TestGrantProfileMode(t *testing.T) {
	// Profile mode: no role ARN in context, --aws-profile is set.
	// We can't exercise the live AWS validation in a unit test, but we can
	// verify the grant() function builds the right credential shape for
	// profile mode by short-circuiting the validation hook.
	origValidate := validateProfileForGrant
	validateProfileForGrant = func(ctx context.Context, profile, region string) error { return nil }
	t.Cleanup(func() { validateProfileForGrant = origValidate })

	ctx := WithGrantOptions(context.Background(), "" /*role*/, "us-west-2", "", "", "corp-broker")
	cred, err := grant(ctx)
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	if cred.Token != "" {
		t.Errorf("Token = %q, want empty for profile mode", cred.Token)
	}
	if got := cred.Metadata[MetaKeySource]; got != "profile" {
		t.Errorf("Metadata[source] = %q, want %q", got, "profile")
	}
	if got := cred.Metadata[MetaKeyProfile]; got != "corp-broker" {
		t.Errorf("Metadata[profile] = %q, want corp-broker", got)
	}
	if got := cred.Metadata[MetaKeyRegion]; got != "us-west-2" {
		t.Errorf("Metadata[region] = %q, want us-west-2", got)
	}
}

func TestGrantRequiresRoleOrProfile(t *testing.T) {
	// Neither role ARN nor profile provided → must error before any AWS call.
	ctx := WithGrantOptions(context.Background(), "", "", "", "", "")
	_, err := grant(ctx)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "role ARN") || !strings.Contains(err.Error(), "--aws-profile") {
		t.Errorf("error message must mention both options: %v", err)
	}
}
```

(`context` should already be imported in `grant_test.go` from Task 1; if not, add it.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/providers/aws/ -run 'TestGrantProfileMode|TestGrantRequiresRoleOrProfile' -v`
Expected: FAIL — `validateProfileForGrant` undefined; profile-mode flow not implemented; bare-grant error not present.

- [ ] **Step 3: Implement mode-aware grant**

In `internal/providers/aws/grant.go`, replace the body of `grant()` to be mode-aware. The new body:

```go
func grant(ctx context.Context) (*provider.Credential, error) {
	var roleARN string
	if v, ok := ctx.Value(ctxKeyRole).(string); ok && v != "" {
		roleARN = v
	}

	var awsProfile string
	if v, ok := ctx.Value(ctxKeyProfile).(string); ok && v != "" {
		awsProfile = v
	} else if v := os.Getenv("AWS_PROFILE"); v != "" {
		awsProfile = v
		ui.Infof("Using AWS profile from AWS_PROFILE: %s (stored with credential)", v)
	}

	var region, sessionDurationStr, externalID string
	if v, ok := ctx.Value(ctxKeyRegion).(string); ok {
		region = v
	}
	if v, ok := ctx.Value(ctxKeySessionDuration).(string); ok {
		sessionDurationStr = v
	}
	if v, ok := ctx.Value(ctxKeyExternalID).(string); ok {
		externalID = v
	}

	// Choose source mode from inputs.
	switch {
	case roleARN != "":
		return grantRoleMode(ctx, roleARN, region, sessionDurationStr, externalID, awsProfile)
	case awsProfile != "":
		return grantProfileMode(ctx, awsProfile, region)
	default:
		return nil, &provider.GrantError{
			Provider: "aws",
			Cause: fmt.Errorf("moat grant aws requires either a role ARN to assume " +
				"or --aws-profile <name> for a profile whose credentials moat should serve directly"),
			Hint: "Examples:\n" +
				"  moat grant aws --role=arn:aws:iam::123456789012:role/MyRole\n" +
				"  moat grant aws --aws-profile=corp-broker\n" +
				"Run 'moat grant aws --help' for the full flag list.",
		}
	}
}

// grantRoleMode is the today-behavior path: AssumeRole the stored role.
func grantRoleMode(ctx context.Context, roleARN, region, sessionDurationStr, externalID, awsProfile string) (*provider.Credential, error) {
	cfg, err := ParseRoleARN(roleARN)
	if err != nil {
		return nil, &provider.GrantError{
			Provider: "aws",
			Cause:    err,
			Hint:     "Example: arn:aws:iam::123456789012:role/MyRole",
		}
	}
	cfg.Source = "role"
	if region != "" {
		cfg.Region = region
	}
	if sessionDurationStr != "" {
		if d, parseErr := time.ParseDuration(sessionDurationStr); parseErr == nil {
			cfg.SessionDuration = d
		}
	}
	if externalID != "" {
		cfg.ExternalID = externalID
	}
	cfg.Profile = awsProfile

	if err := testAssumeRole(ctx, cfg); err != nil {
		return nil, &provider.GrantError{
			Provider: "aws",
			Cause:    err,
			Hint: "Ensure you have permission to assume this role and that your AWS credentials are configured.\n" +
				"See: https://majorcontext.com/moat/concepts/credentials#aws",
		}
	}

	cred := &provider.Credential{
		Provider:  "aws",
		Token:     cfg.RoleARN,
		CreatedAt: time.Now(),
		Metadata: map[string]string{
			MetaKeySource:          "role",
			MetaKeyRegion:          cfg.Region,
			MetaKeySessionDuration: cfg.SessionDuration.String(),
		},
	}
	if cfg.Profile != "" {
		cred.Metadata[MetaKeyProfile] = cfg.Profile
	}
	if cfg.ExternalID != "" {
		cred.Metadata[MetaKeyExternalID] = cfg.ExternalID
	}
	return cred, nil
}

// grantProfileMode is the new pass-through path: serve the named profile's
// resolved credentials directly. No AssumeRole.
func grantProfileMode(ctx context.Context, awsProfile, region string) (*provider.Credential, error) {
	resolvedRegion := DefaultRegion
	if region != "" {
		resolvedRegion = region
	}

	if err := validateProfileForGrant(ctx, awsProfile, resolvedRegion); err != nil {
		return nil, &provider.GrantError{
			Provider: "aws",
			Cause:    err,
			Hint: fmt.Sprintf("The profile %q must resolve to usable AWS credentials.\n"+
				"Verify with: aws --profile %s sts get-caller-identity\n"+
				"If the profile uses credential_process, ensure its command is on PATH.",
				awsProfile, awsProfile),
		}
	}

	cred := &provider.Credential{
		Provider:  "aws",
		Token:     "", // intentionally empty: no role to assume
		CreatedAt: time.Now(),
		Metadata: map[string]string{
			MetaKeySource:  "profile",
			MetaKeyProfile: awsProfile,
			MetaKeyRegion:  resolvedRegion,
		},
	}
	return cred, nil
}

// validateProfileForGrant verifies the named profile resolves to non-empty
// credentials. It is a package-level var so tests can short-circuit it.
//
// Behavior:
//   1. Load the profile via LoadDefaultConfig(WithSharedConfigProfile).
//   2. Call Credentials.Retrieve(ctx) — must succeed and return a non-empty
//      AccessKeyID. Any error is surfaced (almost always actionable).
//   3. Best-effort sts:GetCallerIdentity to echo the identity in the success
//      log. Failure is NOT fatal: some environments block GetCallerIdentity
//      via SCPs but Bedrock still works.
var validateProfileForGrant = func(ctx context.Context, awsProfile, region string) error {
	awsCfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithSharedConfigProfile(awsProfile),
	)
	if err != nil {
		return fmt.Errorf("loading profile %q: %w", awsProfile, err)
	}
	creds, err := awsCfg.Credentials.Retrieve(ctx)
	if err != nil {
		return fmt.Errorf("retrieving credentials from profile %q: %w", awsProfile, err)
	}
	if creds.AccessKeyID == "" {
		return fmt.Errorf("profile %q resolved to empty credentials", awsProfile)
	}

	// Best-effort identity echo.
	stsClient := sts.NewFromConfig(awsCfg)
	out, idErr := stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if idErr != nil {
		ui.Infof("Bound to profile %q (identity unavailable: GetCallerIdentity denied)", awsProfile)
	} else if out != nil && out.Arn != nil {
		ui.Infof("Bound to identity %s (profile %q)", *out.Arn, awsProfile)
	}
	return nil
}
```

Note: `testAssumeRole` (the existing function at ~lines 201-230) is kept as-is — `grantRoleMode` calls it directly. No changes needed there.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/providers/aws/ -run 'TestGrantProfileMode|TestGrantRequiresRoleOrProfile|TestConfigFromCredentialSource' -v`
Expected: all PASS.

Then full package: `go test ./internal/providers/aws/`
Expected: ok.

Then build: `go build ./...`
Expected: clean.

- [ ] **Step 5: Commit**

```bash
git add internal/providers/aws/grant.go internal/providers/aws/grant_test.go
git commit -m "feat(aws): grant supports profile pass-through mode (no AssumeRole)"
```

---

## Task 3: CLI relaxes `--role` requirement when `--aws-profile` is given

**Files:**
- Modify: `cmd/moat/cli/grant.go` (the `if providerName == "aws" && awsRole == "" { ... }` block ~lines 107-117; the help text inside the same block and elsewhere as appropriate)

- [ ] **Step 1: Locate the current gate**

Run: `grep -n "awsRole == \"\"\|aws-profile\|--role" cmd/moat/cli/grant.go`
Expected: the existing block at ~line 107 hard-errors if `awsRole == ""`. Confirm the current help text references `--role` as required.

- [ ] **Step 2: Replace the AWS pre-Grant gate**

In `cmd/moat/cli/grant.go`, replace the `if providerName == "aws" && awsRole == "" { ... }` block with one that accepts either `--role` or `--aws-profile`:

```go
	// For AWS, require either --role (AssumeRole mode) or --aws-profile
	// (pass-through mode). Bare invocation is a footgun (would silently
	// use whatever the daemon host's default credential chain yields).
	if providerName == "aws" && awsRole == "" && awsProfile == "" {
		return fmt.Errorf(`moat grant aws requires either an IAM role ARN to assume or an explicit AWS profile to pass through

Examples:
  moat grant aws --role=arn:aws:iam::ACCOUNT:role/ROLE_NAME
      Stores a role ARN; moat calls sts:AssumeRole each time creds are needed.

  moat grant aws --aws-profile=corp-broker
      Stores the profile name; moat serves the profile's resolved credentials
      directly (the profile's credential_process must already yield usable creds).
      Use this when you have no base IAM identity and your org issues
      role-scoped credentials directly (SSO / credential_process brokers).

Options:
  --role             IAM role ARN to assume (role mode)
  --aws-profile      AWS shared config profile (pass-through mode, or role-mode source)
  --region           AWS region (default: us-east-1)
  --session-duration Session duration (default: 15m, max: 12h; role mode only)
  --external-id      External ID for role assumption (role mode only)`)
	}
```

(No other changes in this file — the existing `ctx = aws.WithGrantOptions(...)` call at ~line 128 already passes `awsProfile`; Task 2's `grant()` interprets the inputs.)

- [ ] **Step 3: Verify build**

Run: `go build ./...`
Expected: clean.

- [ ] **Step 4: Smoke-test the new accepted form (no AWS network call needed)**

Test by inspecting the error path only (we can't easily integration-test the full grant in a hermetic environment because grant calls real AWS APIs — but we CAN verify the CLI accepts the new form by running it and confirming it gets past the pre-Grant validation and into the grant function, which will error somewhere downstream).

Run: `MOAT_DAEMON_DISABLED=1 ./moat grant aws --aws-profile=nonexistent-profile-xyz 2>&1 | head -5` (note: this *will* fail at profile load, but it should fail with a "profile not found" type error from `validateProfileForGrant`, NOT with the CLI-level "—role is required" message).

Since this is a manual smoke test, this step is **only verified by reading the diff**: the implementer must confirm the `if awsRole == "" && awsProfile == ""` gate replaces the old `if awsRole == ""` gate, and the error message references both options. Paste the diff in the commit/report.

- [ ] **Step 5: Commit**

```bash
git add cmd/moat/cli/grant.go
git commit -m "feat(cli): accept --aws-profile without --role for pass-through grant"
```

---

## Task 4: `CredentialProvider` serves profile mode without AssumeRole

**Files:**
- Modify: `internal/providers/aws/credential_provider.go` (`CredentialProviderConfig` struct ~lines 18-25; `CredentialProvider` struct ~lines 29-43; `NewCredentialProvider` ~lines 45-65; `GetCredentials` ~lines 92-145)
- Modify: `internal/providers/aws/credential_provider_test.go`
- Also: `internal/run/manager.go`, `internal/daemon/server.go`, `internal/daemon/persist.go` — the three call sites must pass `Source` (and `Profile`) into `CredentialProviderConfig`

- [ ] **Step 1: Find the three call sites**

Run: `grep -n "awsprov.NewCredentialProvider\|CredentialProviderConfig{" internal/run/manager.go internal/daemon/server.go internal/daemon/persist.go`
Expected: three matches, one per file. Read each to see the existing field set; you'll add `Source: cfg.Source` (and confirm `Profile: cfg.Profile` already flows).

- [ ] **Step 2: Write the failing test**

Append to `internal/providers/aws/credential_provider_test.go`:

```go
func TestCredentialProviderProfileModeSkipsAssumeRole(t *testing.T) {
	// In profile mode, GetCredentials must serve from the AWS SDK credentials
	// provider directly and MUST NOT call sts:AssumeRole.
	failOnAssumeRole := &assumeRoleShouldNotBeCalled{t: t}
	fakeExpires := time.Now().Add(30 * time.Minute)

	p := &CredentialProvider{
		source:          "profile",
		region:          "us-west-2",
		sessionDuration: 15 * time.Minute,
		stsClient:       failOnAssumeRole, // fails the test if invoked
		profileCreds: staticCredentialsProvider{
			creds: aws.Credentials{
				AccessKeyID:     "AKIDPROFILE",
				SecretAccessKey: "SECRET",
				SessionToken:    "TOKEN",
				Expires:         fakeExpires,
				CanExpire:       true,
			},
		},
	}

	got, err := p.GetCredentials(context.Background())
	if err != nil {
		t.Fatalf("GetCredentials: %v", err)
	}
	if got.AccessKeyID != "AKIDPROFILE" {
		t.Errorf("AccessKeyID = %q, want AKIDPROFILE", got.AccessKeyID)
	}
	if got.SessionToken != "TOKEN" {
		t.Errorf("SessionToken = %q, want TOKEN", got.SessionToken)
	}
	if !got.Expiration.Equal(fakeExpires) {
		t.Errorf("Expiration = %v, want %v", got.Expiration, fakeExpires)
	}
}

func TestCredentialProviderProfileModeHandlesNonExpiringSource(t *testing.T) {
	// If the underlying source returns CanExpire=false (e.g., static keys),
	// the provider must still set a finite cached expiration (defensive
	// refresh window) so it re-Retrieves at a sensible cadence.
	p := &CredentialProvider{
		source:          "profile",
		region:          "us-west-2",
		sessionDuration: 15 * time.Minute,
		stsClient:       &assumeRoleShouldNotBeCalled{t: t},
		profileCreds: staticCredentialsProvider{
			creds: aws.Credentials{
				AccessKeyID:     "AKIDSTATIC",
				SecretAccessKey: "SECRET",
				CanExpire:       false, // perpetual
			},
		},
	}
	got, err := p.GetCredentials(context.Background())
	if err != nil {
		t.Fatalf("GetCredentials: %v", err)
	}
	if got.AccessKeyID != "AKIDSTATIC" {
		t.Errorf("AccessKeyID = %q, want AKIDSTATIC", got.AccessKeyID)
	}
	// Verify the provider chose a non-zero, finite expiration to drive refresh.
	if got.Expiration.IsZero() || got.Expiration.After(time.Now().Add(time.Hour)) {
		t.Errorf("Expiration = %v, want a finite near-future time (defensive refresh window)", got.Expiration)
	}
}

// assumeRoleShouldNotBeCalled is an STSAssumeRoler that fails the test if invoked.
type assumeRoleShouldNotBeCalled struct{ t *testing.T }

func (a *assumeRoleShouldNotBeCalled) AssumeRole(_ context.Context, _ *sts.AssumeRoleInput, _ ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
	a.t.Fatal("AssumeRole must not be called in profile mode")
	return nil, nil
}

// staticCredentialsProvider implements aws.CredentialsProvider for tests.
type staticCredentialsProvider struct {
	creds aws.Credentials
}

func (s staticCredentialsProvider) Retrieve(_ context.Context) (aws.Credentials, error) {
	return s.creds, nil
}
```

Imports needed (add to test file if missing): `time`, `context`, `github.com/aws/aws-sdk-go-v2/aws`, `github.com/aws/aws-sdk-go-v2/service/sts`.

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/providers/aws/ -run 'TestCredentialProviderProfileMode' -v`
Expected: FAIL — `CredentialProvider` has no `source` / `profileCreds` field.

- [ ] **Step 4: Implement the profile branch in `CredentialProvider`**

In `internal/providers/aws/credential_provider.go`:

(a) Add `Source` to `CredentialProviderConfig`:

```go
type CredentialProviderConfig struct {
	Source          string // "role" (default) or "profile"
	RoleARN         string
	Region          string
	SessionDuration time.Duration
	ExternalID      string
	Profile         string // AWS shared config profile
}
```

(b) Add fields to `CredentialProvider`:

```go
type CredentialProvider struct {
	source          string                  // "role" or "profile"
	roleARN         string
	region          string
	sessionDuration time.Duration
	externalID      string
	sessionName     string
	authToken       string

	mu         sync.RWMutex
	cached     *Credentials
	expiration time.Time

	// stsClient is used in role mode.
	stsClient STSAssumeRoler
	// profileCreds is used in profile mode (set by NewCredentialProvider from
	// the loaded awsCfg.Credentials, which the SDK wraps in a CredentialsCache
	// that natively handles credential_process / SSO refresh).
	profileCreds aws.CredentialsProvider
}
```

(c) Update `NewCredentialProvider` to populate both, and default `Source` to "role" when empty (for callers that haven't been updated yet):

```go
func NewCredentialProvider(ctx context.Context, cfg CredentialProviderConfig, sessionName string) (*CredentialProvider, error) {
	opts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(cfg.Region)}
	if cfg.Profile != "" {
		opts = append(opts, awsconfig.WithSharedConfigProfile(cfg.Profile))
		slog.Debug("AWS credential provider using named profile",
			"profile", cfg.Profile, "role_arn", cfg.RoleARN, "source", cfg.Source)
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}

	source := cfg.Source
	if source == "" {
		source = "role"
	}
	return &CredentialProvider{
		source:          source,
		roleARN:         cfg.RoleARN,
		region:          cfg.Region,
		sessionDuration: cfg.SessionDuration,
		externalID:      cfg.ExternalID,
		sessionName:     sessionName,
		stsClient:       sts.NewFromConfig(awsCfg),
		profileCreds:    awsCfg.Credentials,
	}, nil
}
```

(d) Replace `GetCredentials` to branch on source. The existing role path is preserved verbatim; add a profile branch using the same cache pattern:

```go
// profileCacheDefault bounds how long we cache profile-mode credentials
// when the underlying source declares CanExpire=false. Avoids both
// hot-pathing the credential_process and pinning forever.
const profileCacheDefault = 15 * time.Minute

func (p *CredentialProvider) GetCredentials(ctx context.Context) (*Credentials, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	p.mu.RLock()
	if p.cached != nil && time.Now().Add(credentialRefreshBuffer).Before(p.expiration) {
		creds := p.cached
		p.mu.RUnlock()
		return creds, nil
	}
	p.mu.RUnlock()

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cached != nil && time.Now().Add(credentialRefreshBuffer).Before(p.expiration) {
		return p.cached, nil
	}

	switch p.source {
	case "profile":
		return p.fetchFromProfile(ctx)
	default: // "role"
		return p.fetchViaAssumeRole(ctx)
	}
}

func (p *CredentialProvider) fetchViaAssumeRole(ctx context.Context) (*Credentials, error) {
	input := &sts.AssumeRoleInput{
		RoleArn:         awssdk.String(p.roleARN),
		RoleSessionName: awssdk.String(p.sessionName),
		DurationSeconds: awssdk.Int32(int32(p.sessionDuration.Seconds())),
	}
	if p.externalID != "" {
		input.ExternalId = awssdk.String(p.externalID)
	}
	result, err := p.stsClient.AssumeRole(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("assuming role %s: %w", p.roleARN, err)
	}
	if result.Credentials == nil {
		return nil, fmt.Errorf("AWS returned empty credentials for role %s", p.roleARN)
	}
	p.cached = &Credentials{
		AccessKeyID:     awssdk.ToString(result.Credentials.AccessKeyId),
		SecretAccessKey: awssdk.ToString(result.Credentials.SecretAccessKey),
		SessionToken:    awssdk.ToString(result.Credentials.SessionToken),
		Expiration:      awssdk.ToTime(result.Credentials.Expiration),
	}
	p.expiration = awssdk.ToTime(result.Credentials.Expiration)
	return p.cached, nil
}

func (p *CredentialProvider) fetchFromProfile(ctx context.Context) (*Credentials, error) {
	if p.profileCreds == nil {
		return nil, fmt.Errorf("profile credentials provider is nil")
	}
	creds, err := p.profileCreds.Retrieve(ctx)
	if err != nil {
		return nil, fmt.Errorf("retrieving credentials from profile: %w", err)
	}
	if creds.AccessKeyID == "" {
		return nil, fmt.Errorf("profile returned empty credentials")
	}
	exp := creds.Expires
	if !creds.CanExpire || exp.IsZero() {
		// Source declares no expiration (e.g., static keys). Bound the cache
		// so we still re-Retrieve at a sensible cadence; the SDK
		// CredentialsCache will answer most re-Retrieves from its own cache
		// when the underlying provider is non-expiring.
		exp = time.Now().Add(profileCacheDefault)
	}
	p.cached = &Credentials{
		AccessKeyID:     creds.AccessKeyID,
		SecretAccessKey: creds.SecretAccessKey,
		SessionToken:    creds.SessionToken,
		Expiration:      exp,
	}
	p.expiration = exp
	return p.cached, nil
}
```

Required new import in `credential_provider.go`: add `"github.com/aws/aws-sdk-go-v2/aws"` if not already imported under a different alias. The existing file imports `awssdk "github.com/aws/aws-sdk-go-v2/aws"` — reuse that alias: the `aws.CredentialsProvider` interface and `aws.Credentials` type used above would be `awssdk.CredentialsProvider` / `awssdk.Credentials`. **Update the code blocks in this Step 4 to use `awssdk` instead of `aws` for those type references** so the file's existing alias is preserved consistently.

Likewise, update the test file `credential_provider_test.go` to use whatever `aws` import alias it currently uses (read the test's import block first). If neither alias is present yet, add `import "github.com/aws/aws-sdk-go-v2/aws"` to the test file and use the unaliased `aws.Credentials`/`aws.CredentialsProvider`.

(e) **Update the three callers to pass `Source`.** In each of `internal/run/manager.go` (~line 911), `internal/daemon/server.go` (~line 233), and `internal/daemon/persist.go` (~line 232), the call constructs an `awsprov.CredentialProviderConfig{...}` literal from a `*aws.Config` (the result of `awsprov.ConfigFromCredential`). Add `Source: cfg.Source,` to each struct literal (the variable name may differ — read each site, locate the existing fields like `RoleARN: cfg.RoleARN`, and add `Source` alongside). Use exactly the field assignment idiom already in use at that site.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/providers/aws/ -run 'TestCredentialProviderProfileMode' -v`
Expected: PASS.

Run full package: `go test ./internal/providers/aws/ ./internal/run/ ./internal/daemon/`
Expected: ok (no regressions; existing role-mode tests must still pass).

Run: `go build ./...`
Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add internal/providers/aws/ internal/run/manager.go internal/daemon/server.go internal/daemon/persist.go
git commit -m "feat(aws): serve profile-mode credentials without AssumeRole"
```

---

## Task 5: Two-implementations reconciliation

**Files:**
- Read first: `internal/providers/aws/provider.go` (`RegisterEndpoints` ~lines 67-71), `internal/providers/aws/endpoint.go` (the entire `EndpointHandler`), `internal/daemon/*.go` (callers of `RegisterEndpoints`)
- Modify or delete: `internal/providers/aws/endpoint.go`, `internal/providers/aws/endpoint_test.go`, `internal/providers/aws/provider.go`

This task collapses the two AWS credential serving paths into one.

- [ ] **Step 1: Determine if `EndpointHandler` has a live caller**

Run:
```bash
grep -rn "RegisterEndpoints\|/aws-credentials" --include='*.go' .
```

Read each match. The container-facing path is `/_aws/credentials` (registered by the daemon's run-handler logic and served via `CredentialProvider.Handler()`). `RegisterEndpoints` registers a DIFFERENT path `/aws-credentials` on whatever mux the EndpointProvider interface wires it to.

Determine: does anything in the live daemon mux actually invoke `Provider.RegisterEndpoints` for AWS at runtime? (Search for callers of the `EndpointProvider` interface's `RegisterEndpoints` method.)

Run:
```bash
grep -rn "EndpointProvider\|\.RegisterEndpoints(" --include='*.go' internal/
```

- [ ] **Step 2: Implement the chosen reconciliation**

**Branch A — `EndpointHandler` has no live caller (most likely):**

Delete the dead code:

1. Delete `internal/providers/aws/endpoint.go` and `internal/providers/aws/endpoint_test.go`.
2. In `internal/providers/aws/provider.go`:
   - Remove `RegisterEndpoints` method.
   - Remove the `_ provider.EndpointProvider = (*Provider)(nil)` compile-time assertion.
   - If the file imports `net/http` solely for the deleted method, drop that import.

**Branch B — `EndpointHandler` IS used (less likely):**

Route it through `CredentialProvider` so there is one acquisition path. Replace the body of `RegisterEndpoints` in `provider.go`:

```go
func (p *Provider) RegisterEndpoints(mux *http.ServeMux, cred *provider.Credential) {
	cfg, err := ConfigFromCredential(cred)
	if err != nil {
		// Server-side log; we cannot return an error from RegisterEndpoints.
		slog.Error("AWS RegisterEndpoints: parsing credential", "error", err)
		return
	}
	provCfg := CredentialProviderConfig{
		Source:          cfg.Source,
		RoleARN:         cfg.RoleARN,
		Region:          cfg.Region,
		SessionDuration: cfg.SessionDuration,
		ExternalID:      cfg.ExternalID,
		Profile:         cfg.Profile,
	}
	cp, err := NewCredentialProvider(context.Background(), provCfg, "moat-endpoint")
	if err != nil {
		slog.Error("AWS RegisterEndpoints: creating provider", "error", err)
		return
	}
	mux.Handle("/aws-credentials", cp.Handler())
}
```

And then delete `internal/providers/aws/endpoint.go` and `endpoint_test.go` as in Branch A — `EndpointHandler` is replaced, not retained.

- [ ] **Step 3: Verify build + tests**

Run: `go build ./...`
Expected: clean.

Run: `go test ./internal/providers/aws/ ./internal/run/ ./internal/daemon/`
Expected: ok.

Run: `make lint`
Expected: 0 issues.

- [ ] **Step 4: Commit**

```bash
git add internal/providers/aws/
git commit -m "refactor(aws): collapse EndpointHandler into CredentialProvider"
```

Use exactly this commit message regardless of which branch (A or B) was taken — the user-visible result is the same.

---

## Task 6: Documentation + changelog

**Files:**
- Modify: `docs/content/reference/01-cli.md` (the `moat grant aws` section)
- Modify: `docs/content/reference/04-grants.md` (the AWS section)
- Modify: `docs/content/guides/14-claude-bedrock.md` (Prerequisites → mention pass-through for no-AssumeRole environments)
- Modify: `CHANGELOG.md`
- Modify: `internal/providers/aws/doc.go` (one-paragraph update)

- [ ] **Step 1: CLI reference**

In `docs/content/reference/01-cli.md`, in the `moat grant aws` subsection, document both invocations. Match the file's existing CLI-section format (read the surrounding entries — e.g. `moat grant anthropic`/`moat grant github` — and mirror the heading depth, example block style, and flag table). Cover:

- The role-mode form (`--role=<arn>`, today's behavior, unchanged).
- The new profile-mode form (`--aws-profile=<name>`, no `--role`): "moat serves the profile's resolved credentials directly without calling `sts:AssumeRole`. Use this when your environment issues role-scoped credentials via a `credential_process` broker and AssumeRole is not available."
- The hard-error case (neither given) and its exact message.
- The security trade-off: in profile mode, CloudTrail entries are attributed to the profile's identity with no `moat-<…>` session-name discriminator; revocation moves to the upstream broker.

- [ ] **Step 2: Grants reference**

In `docs/content/reference/04-grants.md`, in the AWS grant section (the one with the `## AWS` heading we confirmed at line 534 in Task 10 of the prior plan), add a "Source modes" subsection documenting the `source: role | profile` metadata field, the invariants (role mode requires Token; profile mode requires `profile` metadata and empty Token; missing key defaults to `role`), and link to the CLI reference for the flags.

- [ ] **Step 3: Bedrock guide cross-link**

In `docs/content/guides/14-claude-bedrock.md`, in the **Prerequisites** section, add a brief paragraph for environments without AssumeRole capability:

> If your environment issues role-scoped credentials via a broker tool (no base IAM identity, no `sts:AssumeRole` permission), use `moat grant aws --aws-profile <name>` against a profile whose `credential_process` calls your broker. moat will serve those credentials directly, skipping its own `AssumeRole` step. See `moat grant aws --help` and the [grants reference](../reference/04-grants.md#aws) for details.

(Keep it short — one paragraph. The detail lives in the references.)

- [ ] **Step 4: Changelog**

In `CHANGELOG.md`, under the current `## Unreleased` section's `### Added` (matching the existing entry style — bold feature name, `#NNN` PR placeholder):

```markdown
- **AWS credential pass-through** — `moat grant aws --aws-profile <name>` (no `--role`) stores a profile-based credential; moat serves the profile's resolved credentials directly without calling `sts:AssumeRole`. Enables Claude-on-Bedrock and other AWS consumers in environments where the operator has no usable base IAM identity (SSO / `credential_process` brokers issuing role-scoped credentials directly). ([#NNN](https://github.com/majorcontext/moat/pull/NNN))
```

- [ ] **Step 5: Package doc**

In `internal/providers/aws/doc.go`, update the package-level doc paragraph to describe both source modes:

- Add one sentence: "Credentials are acquired in one of two modes selected by the `source` metadata key: `role` (default — moat calls `sts:AssumeRole` on the stored role ARN) or `profile` (moat serves the named AWS shared-config profile's resolved credentials directly, without AssumeRole; useful when the profile's `credential_process` already yields role-scoped credentials)."

- [ ] **Step 6: Verify and commit**

Run: `git status` and confirm only the 5 doc files (4 markdown + `doc.go`) are modified (no production code).

```bash
git add docs/ CHANGELOG.md internal/providers/aws/doc.go
git commit -m "docs(aws): document profile-source pass-through mode"
```

---

## Final verification (run before opening a PR)

- [ ] `go build ./...` — exit 0
- [ ] `make test-unit` — full suite green with race detector (the pre-existing `internal/deps` network failure in restricted-egress environments is acceptable per the prior plan's same finding)
- [ ] `make lint` — clean (0 issues)
- [ ] Manually verify the new pass-through path against a real `credential_process` profile (out of CI scope; document in the guide if not already)
- [ ] Re-read spec §3.2 (`source` invariants), §3.4 (single-implementation post-change), §3.5 (best-effort `GetCallerIdentity`), §3.6 (missing `source` defaults to `role`) and confirm each maps to landed code.
- [ ] Use `superpowers:finishing-a-development-branch` to decide merge/PR.

---

## Self-Review (completed during planning)

**Spec coverage:**

| Spec § | Implemented by |
|---|---|
| §3.1 grant UX (role | profile | neither→error) | Task 2 (`grant()` mode dispatch); Task 3 (CLI gate + error message) |
| §3.2 stored credential schema, `MetaKeySource`, invariants, missing-key default | Task 1 |
| §3.3 live serving path branch in `CredentialProvider` | Task 4 |
| §3.4 two-implementations reconciliation | Task 5 (with call-graph determination) |
| §3.5 mode-aware pre-save validation; best-effort GetCallerIdentity | Task 2 (`validateProfileForGrant`) |
| §3.6 daemon API additive-only | Task 1 (single optional metadata key; old creds default-source `role`) |
| §3.7 Bedrock unchanged | No code task (verified by no Bedrock files touched); doc cross-link in Task 6 |
| §3.8 security trade-offs documented | Task 6 (CLI ref + grants ref) |
| Testing | Tests embedded in Tasks 1, 2, 4 |
| Files-to-change | All listed in the File Structure table |

No gaps.

**Placeholder scan:** Task 5 has two branches (A delete / B route) explicitly determined by Step 1's grep result — that is a bounded investigation with prescribed action per outcome, not a placeholder. Task 3 Step 4 is "verify by reading the diff" because the CLI grant calls real AWS and is not unit-testable here — that's an honest limit, not a skipped step. `#NNN` PR placeholder in the CHANGELOG is intentional (filled at PR time). No "TBD"/"fix later"/"similar to" placeholders.

**Type consistency:** `Config.Source string`, `CredentialProviderConfig.Source string`, `CredentialProvider.source string`, `MetaKeySource = "source"`, source values `"role"` / `"profile"` — used identically across Tasks 1, 2, 4, 5. `validateProfileForGrant` (Task 2) is the only test-injectable hook. `grantRoleMode`/`grantProfileMode` (Task 2) are the only new internal functions. `aws.CredentialsProvider` / `awssdk.CredentialsProvider` alias usage flagged explicitly in Task 4 to avoid drift.
