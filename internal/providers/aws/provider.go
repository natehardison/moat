package aws

import (
	"context"

	"github.com/majorcontext/moat/internal/provider"
)

// Provider implements provider.CredentialProvider for AWS credentials.
type Provider struct{}

// Compile-time interface assertions.
var (
	_ provider.CredentialProvider = (*Provider)(nil)
)

// New creates a new AWS provider.
func New() *Provider {
	return &Provider{}
}

func init() {
	provider.Register(New())
}

// Name returns the provider identifier.
func (p *Provider) Name() string {
	return "aws"
}

// Grant acquires AWS credentials in one of two modes (role-assumption or profile pass-through), selected by the inputs in the request context.
func (p *Provider) Grant(ctx context.Context) (*provider.Credential, error) {
	return grant(ctx)
}

// ConfigureProxy is a no-op for AWS; credentials are served via the
// CredentialProvider endpoint, not header injection.
func (p *Provider) ConfigureProxy(pc provider.ProxyConfigurer, cred *provider.Credential) {
	// No-op: AWS uses credential endpoint, not proxy header injection
}

// ContainerEnv returns nil; the run manager sets AWS_CONTAINER_CREDENTIALS_FULL_URI.
func (p *Provider) ContainerEnv(cred *provider.Credential) []string {
	// The run manager configures AWS_CONTAINER_CREDENTIALS_FULL_URI
	// pointing to the proxy's credential endpoint.
	return nil
}

// ContainerMounts returns nil; AWS doesn't require any mounts.
func (p *Provider) ContainerMounts(cred *provider.Credential, containerHome string) ([]provider.MountConfig, string, error) {
	return nil, "", nil
}

// Cleanup is a no-op for AWS.
func (p *Provider) Cleanup(cleanupPath string) {
	// No cleanup needed
}

// ImpliedDependencies returns dependencies implied by AWS grant.
func (p *Provider) ImpliedDependencies() []string {
	return []string{"aws"}
}
