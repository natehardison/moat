package aws

import (
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/provider"
)

func TestProvider_Name(t *testing.T) {
	p := New()
	if got := p.Name(); got != "aws" {
		t.Errorf("Name() = %q, want %q", got, "aws")
	}
}

func TestProvider_ImpliedDependencies(t *testing.T) {
	p := New()
	deps := p.ImpliedDependencies()
	if len(deps) != 1 || deps[0] != "aws" {
		t.Errorf("ImpliedDependencies() = %v, want [aws]", deps)
	}
}

func TestProvider_ContainerEnv(t *testing.T) {
	p := New()
	cred := &provider.Credential{Token: "arn:aws:iam::123456789012:role/Test"}
	env := p.ContainerEnv(cred)
	if env != nil {
		t.Errorf("ContainerEnv() = %v, want nil", env)
	}
}

func TestProvider_ContainerMounts(t *testing.T) {
	p := New()
	cred := &provider.Credential{Token: "arn:aws:iam::123456789012:role/Test"}
	mounts, cleanupPath, err := p.ContainerMounts(cred, "/home/user")
	if err != nil {
		t.Errorf("ContainerMounts() error = %v", err)
	}
	if mounts != nil {
		t.Errorf("ContainerMounts() mounts = %v, want nil", mounts)
	}
	if cleanupPath != "" {
		t.Errorf("ContainerMounts() cleanupPath = %q, want empty", cleanupPath)
	}
}

func TestParseRoleARN(t *testing.T) {
	tests := []struct {
		name    string
		arn     string
		wantARN string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid ARN",
			arn:     "arn:aws:iam::123456789012:role/MyRole",
			wantARN: "arn:aws:iam::123456789012:role/MyRole",
		},
		{
			name:    "valid ARN with path",
			arn:     "arn:aws:iam::123456789012:role/admin/MyAdminRole",
			wantARN: "arn:aws:iam::123456789012:role/admin/MyAdminRole",
		},
		{
			name:    "valid ARN aws-cn partition",
			arn:     "arn:aws-cn:iam::123456789012:role/MyRole",
			wantARN: "arn:aws-cn:iam::123456789012:role/MyRole",
		},
		{
			name:    "valid ARN aws-us-gov partition",
			arn:     "arn:aws-us-gov:iam::123456789012:role/MyRole",
			wantARN: "arn:aws-us-gov:iam::123456789012:role/MyRole",
		},
		{
			name:    "empty ARN",
			arn:     "",
			wantErr: true,
			errMsg:  "role ARN is required",
		},
		{
			name:    "not enough parts",
			arn:     "arn:aws:iam",
			wantErr: true,
			errMsg:  "expected 6 colon-separated parts",
		},
		{
			name:    "wrong prefix",
			arn:     "arm:aws:iam::123456789012:role/MyRole",
			wantErr: true,
			errMsg:  "must start with 'arn:'",
		},
		{
			name:    "invalid partition",
			arn:     "arn:aws-invalid:iam::123456789012:role/MyRole",
			wantErr: true,
			errMsg:  "invalid ARN partition",
		},
		{
			name:    "not IAM service",
			arn:     "arn:aws:s3::123456789012:role/MyRole",
			wantErr: true,
			errMsg:  "must be an IAM ARN",
		},
		{
			name:    "missing account ID",
			arn:     "arn:aws:iam:::role/MyRole",
			wantErr: true,
			errMsg:  "account ID is required",
		},
		{
			name:    "not a role",
			arn:     "arn:aws:iam::123456789012:user/MyUser",
			wantErr: true,
			errMsg:  "must be a role ARN",
		},
		{
			name:    "role without name",
			arn:     "arn:aws:iam::123456789012:role/",
			wantErr: true,
			errMsg:  "role name is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := ParseRoleARN(tt.arn)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseRoleARN(%q) = nil error, want error containing %q", tt.arn, tt.errMsg)
					return
				}
				if tt.errMsg != "" && !containsString(err.Error(), tt.errMsg) {
					t.Errorf("ParseRoleARN(%q) error = %q, want error containing %q", tt.arn, err.Error(), tt.errMsg)
				}
				return
			}
			if err != nil {
				t.Errorf("ParseRoleARN(%q) unexpected error: %v", tt.arn, err)
				return
			}
			if cfg.RoleARN != tt.wantARN {
				t.Errorf("ParseRoleARN(%q).RoleARN = %q, want %q", tt.arn, cfg.RoleARN, tt.wantARN)
			}
			if cfg.Region != DefaultRegion {
				t.Errorf("ParseRoleARN(%q).Region = %q, want %q", tt.arn, cfg.Region, DefaultRegion)
			}
		})
	}
}

func TestConfigFromCredential(t *testing.T) {
	t.Run("basic credential", func(t *testing.T) {
		cred := &provider.Credential{
			Token: "arn:aws:iam::123456789012:role/Test",
		}
		cfg, err := ConfigFromCredential(cred)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.RoleARN != cred.Token {
			t.Errorf("RoleARN = %q, want %q", cfg.RoleARN, cred.Token)
		}
		if cfg.Region != DefaultRegion {
			t.Errorf("Region = %q, want %q", cfg.Region, DefaultRegion)
		}
		if cfg.SessionDuration != DefaultSessionDuration {
			t.Errorf("SessionDuration = %v, want %v", cfg.SessionDuration, DefaultSessionDuration)
		}
	})

	t.Run("with metadata", func(t *testing.T) {
		cred := &provider.Credential{
			Token: "arn:aws:iam::123456789012:role/Test",
			Metadata: map[string]string{
				MetaKeyRegion:          "eu-west-1",
				MetaKeySessionDuration: "1h",
				MetaKeyExternalID:      "ext-123",
				MetaKeyProfile:         "my-profile",
			},
		}
		cfg, err := ConfigFromCredential(cred)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Region != "eu-west-1" {
			t.Errorf("Region = %q, want %q", cfg.Region, "eu-west-1")
		}
		if cfg.SessionDuration != time.Hour {
			t.Errorf("SessionDuration = %v, want %v", cfg.SessionDuration, time.Hour)
		}
		if cfg.ExternalID != "ext-123" {
			t.Errorf("ExternalID = %q, want %q", cfg.ExternalID, "ext-123")
		}
		if cfg.Profile != "my-profile" {
			t.Errorf("Profile = %q, want %q", cfg.Profile, "my-profile")
		}
	})

	t.Run("nil credential", func(t *testing.T) {
		_, err := ConfigFromCredential(nil)
		if err == nil {
			t.Error("expected error for nil credential")
		}
	})

	t.Run("invalid duration", func(t *testing.T) {
		cred := &provider.Credential{
			Token: "arn:aws:iam::123456789012:role/Test",
			Metadata: map[string]string{
				MetaKeySessionDuration: "invalid",
			},
		}
		_, err := ConfigFromCredential(cred)
		if err == nil {
			t.Error("expected error for invalid duration")
		}
	})

	t.Run("legacy scopes format", func(t *testing.T) {
		// Old credentials stored config in Scopes array: [region, sessionDuration, externalID]
		cred := &provider.Credential{
			Token:  "arn:aws:iam::123456789012:role/Test",
			Scopes: []string{"ap-southeast-2", "30m", "legacy-ext-id"},
		}
		cfg, err := ConfigFromCredential(cred)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Region != "ap-southeast-2" {
			t.Errorf("Region = %q, want %q (from legacy Scopes)", cfg.Region, "ap-southeast-2")
		}
		if cfg.SessionDuration != 30*time.Minute {
			t.Errorf("SessionDuration = %v, want %v (from legacy Scopes)", cfg.SessionDuration, 30*time.Minute)
		}
		if cfg.ExternalID != "legacy-ext-id" {
			t.Errorf("ExternalID = %q, want %q (from legacy Scopes)", cfg.ExternalID, "legacy-ext-id")
		}
	})

	t.Run("metadata takes precedence over legacy scopes", func(t *testing.T) {
		// When both are present, Metadata should win
		cred := &provider.Credential{
			Token:  "arn:aws:iam::123456789012:role/Test",
			Scopes: []string{"ap-southeast-2", "30m", "legacy-ext-id"},
			Metadata: map[string]string{
				MetaKeyRegion:          "eu-central-1",
				MetaKeySessionDuration: "2h",
				MetaKeyExternalID:      "new-ext-id",
			},
		}
		cfg, err := ConfigFromCredential(cred)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Region != "eu-central-1" {
			t.Errorf("Region = %q, want %q (from Metadata)", cfg.Region, "eu-central-1")
		}
		if cfg.SessionDuration != 2*time.Hour {
			t.Errorf("SessionDuration = %v, want %v (from Metadata)", cfg.SessionDuration, 2*time.Hour)
		}
		if cfg.ExternalID != "new-ext-id" {
			t.Errorf("ExternalID = %q, want %q (from Metadata)", cfg.ExternalID, "new-ext-id")
		}
	})
}

// containsString checks if s contains substr.
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStringHelper(s, substr))
}

func containsStringHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
