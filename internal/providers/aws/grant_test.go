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
