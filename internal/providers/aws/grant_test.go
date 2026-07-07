package aws

import (
	"context"
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
