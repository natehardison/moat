package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/majorcontext/moat/internal/provider"
	"github.com/majorcontext/moat/internal/ui"
)

// Metadata keys for AWS credentials.
const (
	MetaKeyRegion          = "region"
	MetaKeySessionDuration = "session_duration"
	MetaKeyExternalID      = "external_id"
	MetaKeyProfile         = "profile"
	// MetaKeySource selects the credential acquisition path:
	//   "role"    (default): moat calls sts:AssumeRole on the stored RoleARN.
	//   "profile" (new):     moat serves the named profile's resolved creds directly.
	MetaKeySource = "source"
)

// Default values.
const (
	DefaultRegion          = "us-east-1"
	DefaultSessionDuration = 15 * time.Minute
)

// Context keys for passing grant options from CLI.
type ctxKey string

const (
	ctxKeyRole            ctxKey = "aws_role"
	ctxKeyRegion          ctxKey = "aws_region"
	ctxKeySessionDuration ctxKey = "aws_session_duration"
	ctxKeyExternalID      ctxKey = "aws_external_id"
	ctxKeyProfile         ctxKey = "aws_profile"
)

// WithGrantOptions returns a context with AWS grant options set.
// These options are used by Grant() to determine which credential acquisition mode to use.
func WithGrantOptions(ctx context.Context, role, region, sessionDuration, externalID, profile string) context.Context {
	ctx = context.WithValue(ctx, ctxKeyRole, role)
	ctx = context.WithValue(ctx, ctxKeyRegion, region)
	ctx = context.WithValue(ctx, ctxKeySessionDuration, sessionDuration)
	ctx = context.WithValue(ctx, ctxKeyExternalID, externalID)
	ctx = context.WithValue(ctx, ctxKeyProfile, profile)
	return ctx
}

// Config holds AWS IAM role configuration.
type Config struct {
	RoleARN         string
	Region          string
	SessionDuration time.Duration
	ExternalID      string
	Profile         string // AWS shared config profile (AWS_PROFILE) used to assume the role
	Source          string // "role" (default, AssumeRole) | "profile" (serve profile creds directly, no AssumeRole)
}

// grant acquires AWS credentials in one of two modes:
//   - role mode: assumes the given IAM role via sts:AssumeRole.
//   - profile mode: serves the named AWS profile's resolved credentials directly.
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
	if v, ok := ctx.Value(ctxKeyRegion).(string); ok && v != "" {
		region = v
	}
	if v, ok := ctx.Value(ctxKeySessionDuration).(string); ok && v != "" {
		sessionDurationStr = v
	}
	if v, ok := ctx.Value(ctxKeyExternalID).(string); ok && v != "" {
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

// grantRoleMode assumes the given IAM role via sts:AssumeRole and stores the role ARN as the credential token.
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
//  1. Load the profile via LoadDefaultConfig(WithSharedConfigProfile).
//  2. Call Credentials.Retrieve(ctx) — must succeed and return a non-empty
//     AccessKeyID. Any error is surfaced (almost always actionable).
//  3. Best-effort sts:GetCallerIdentity to echo the identity in the success
//     log. Failure is NOT fatal: some environments block GetCallerIdentity
//     via SCPs but Bedrock still works.
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

// ParseRoleARN validates an IAM role ARN and returns a Config.
// ARN format: arn:PARTITION:iam::ACCOUNT_ID:role/ROLE_NAME
// Supported partitions: aws, aws-cn, aws-us-gov
func ParseRoleARN(arn string) (*Config, error) {
	if arn == "" {
		return nil, fmt.Errorf("role ARN is required")
	}

	parts := strings.Split(arn, ":")
	if len(parts) != 6 {
		return nil, fmt.Errorf("invalid ARN format: expected 6 colon-separated parts, got %d", len(parts))
	}

	prefix, partition, service, _, account, resource := parts[0], parts[1], parts[2], parts[3], parts[4], parts[5]

	if prefix != "arn" {
		return nil, fmt.Errorf("invalid ARN: must start with 'arn:'")
	}

	// Validate partition
	switch partition {
	case "aws", "aws-cn", "aws-us-gov":
		// valid
	default:
		return nil, fmt.Errorf("invalid ARN partition: %s (expected aws, aws-cn, or aws-us-gov)", partition)
	}

	if service != "iam" {
		return nil, fmt.Errorf("invalid ARN: must be an IAM ARN (got %s)", service)
	}

	if account == "" {
		return nil, fmt.Errorf("invalid ARN: account ID is required")
	}

	if !strings.HasPrefix(resource, "role/") {
		return nil, fmt.Errorf("invalid ARN: must be a role ARN (got %s)", resource)
	}

	roleName := strings.TrimPrefix(resource, "role/")
	if roleName == "" {
		return nil, fmt.Errorf("invalid ARN: role name is required")
	}

	return &Config{
		RoleARN:         arn,
		Region:          DefaultRegion,
		SessionDuration: DefaultSessionDuration,
	}, nil
}

// testAssumeRole verifies the role can be assumed with current AWS credentials.
func testAssumeRole(ctx context.Context, cfg *Config) error {
	// Load AWS config from environment, using the explicit profile if set.
	opts := []func(*config.LoadOptions) error{config.WithRegion(cfg.Region)}
	if cfg.Profile != "" {
		opts = append(opts, config.WithSharedConfigProfile(cfg.Profile))
	}
	awsCfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return fmt.Errorf("loading AWS config: %w", err)
	}

	stsClient := sts.NewFromConfig(awsCfg)

	input := &sts.AssumeRoleInput{
		RoleArn:         aws.String(cfg.RoleARN),
		RoleSessionName: aws.String("moat-grant-test"),
		DurationSeconds: aws.Int32(int32(cfg.SessionDuration.Seconds())),
	}

	if cfg.ExternalID != "" {
		input.ExternalId = aws.String(cfg.ExternalID)
	}

	_, err = stsClient.AssumeRole(ctx, input)
	if err != nil {
		return fmt.Errorf("assuming role: %w", err)
	}

	return nil
}

// ConfigFromCredential extracts Config from a stored credential.
// Supports both new format (Metadata) and legacy format (Scopes) for backwards compatibility.
func ConfigFromCredential(cred *provider.Credential) (*Config, error) {
	if cred == nil {
		return nil, fmt.Errorf("credential is nil")
	}

	cfg := &Config{
		RoleARN: cred.Token,
		Region:  DefaultRegion,
	}

	// Try new Metadata format first
	if cred.Metadata != nil {
		if region := cred.Metadata[MetaKeyRegion]; region != "" {
			cfg.Region = region
		}

		if durationStr := cred.Metadata[MetaKeySessionDuration]; durationStr != "" {
			d, err := time.ParseDuration(durationStr)
			if err != nil {
				return nil, fmt.Errorf("invalid session duration %q: %w", durationStr, err)
			}
			cfg.SessionDuration = d
		}

		if externalID := cred.Metadata[MetaKeyExternalID]; externalID != "" {
			cfg.ExternalID = externalID
		}
		if profile := cred.Metadata[MetaKeyProfile]; profile != "" {
			cfg.Profile = profile
		}
		if s := cred.Metadata[MetaKeySource]; s != "" {
			cfg.Source = s
		}
	}

	// Resolve source mode (defaults to "role" for backward compatibility with
	// credentials stored before this field existed).
	if cfg.Source == "" {
		cfg.Source = "role"
	}

	switch cfg.Source {
	case "role":
		if cfg.RoleARN == "" {
			return nil, fmt.Errorf("source=role requires a role ARN")
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

	// Fallback to legacy Scopes format: [region, sessionDuration, externalID]
	if cfg.Region == DefaultRegion && len(cred.Scopes) > 0 && cred.Scopes[0] != "" {
		cfg.Region = cred.Scopes[0]
	}
	if cfg.SessionDuration == 0 && len(cred.Scopes) > 1 && cred.Scopes[1] != "" {
		d, err := time.ParseDuration(cred.Scopes[1])
		if err == nil {
			cfg.SessionDuration = d
		}
	}
	if cfg.ExternalID == "" && len(cred.Scopes) > 2 {
		cfg.ExternalID = cred.Scopes[2]
	}

	if cfg.SessionDuration == 0 {
		cfg.SessionDuration = DefaultSessionDuration
	}

	return cfg, nil
}

// ConfigToJSON serializes Config to JSON for storage.
func ConfigToJSON(cfg *Config) (string, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("marshaling AWS config: %w", err)
	}
	return string(data), nil
}
