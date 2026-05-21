package configprovider

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/majorcontext/moat/internal/config"
	"gopkg.in/yaml.v3"
)

// LoadEmbeddedDef reads and parses the embedded YAML definition for the
// given provider name. Returns an error if no embedded default exists
// (e.g. for Go-implemented providers like github).
func LoadEmbeddedDef(name string) (ProviderDef, error) {
	data, err := defaultsFS.ReadFile("defaults/" + name + ".yaml")
	if err != nil {
		return ProviderDef{}, fmt.Errorf("no embedded provider named %q", name)
	}
	def, err := parseProviderDef(data)
	if err != nil {
		return ProviderDef{}, fmt.Errorf("parsing embedded provider %q: %w", name, err)
	}
	return def, nil
}

// UserOverridePath returns the canonical path for a provider's user-level
// override YAML file under <GlobalConfigDir>/providers/.
func UserOverridePath(name string) string {
	return filepath.Join(config.GlobalConfigDir(), "providers", name+".yaml")
}

// ApplyHostOverride returns a copy of def with Hosts replaced by [host] and
// Validate.URL rewritten so its host component matches the user's host.
// The original def is not mutated. Pure function — no I/O.
func ApplyHostOverride(def ProviderDef, host string) (ProviderDef, error) {
	out := def
	out.Hosts = []string{host}

	if def.Validate != nil {
		u, err := url.Parse(def.Validate.URL)
		if err != nil {
			return ProviderDef{}, fmt.Errorf("parsing validate URL %q: %w", def.Validate.URL, err)
		}
		if u.Host == "" {
			return ProviderDef{}, fmt.Errorf("validate URL %q has no host", def.Validate.URL)
		}
		if u.User != nil {
			return ProviderDef{}, fmt.Errorf("validate URL %q has userinfo, not supported", def.Validate.URL)
		}
		// Replace only the host portion in the raw URL string to preserve any
		// placeholder tokens (e.g. ${token}) that url.String() would percent-encode.
		oldPrefix := u.Scheme + "://" + u.Host
		newPrefix := u.Scheme + "://" + host
		validateCopy := *def.Validate
		validateCopy.URL = newPrefix + strings.TrimPrefix(def.Validate.URL, oldPrefix)
		out.Validate = &validateCopy
	}

	return out, nil
}

// WriteUserOverride marshals def to YAML and writes it to UserOverridePath(name),
// creating the providers directory if it does not already exist.
func WriteUserOverride(name string, def ProviderDef) error {
	path := UserOverridePath(name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating providers dir: %w", err)
	}
	data, err := yaml.Marshal(def)
	if err != nil {
		return fmt.Errorf("marshaling provider def: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing override file %s: %w", path, err)
	}
	return nil
}

// EmbeddedProviderNames returns the sorted list of provider names that
// ship as embedded YAML and therefore support host override via --host.
func EmbeddedProviderNames() []string {
	entries, err := defaultsFS.ReadDir("defaults")
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		names = append(names, strings.TrimSuffix(entry.Name(), ".yaml"))
	}
	sort.Strings(names)
	return names
}

// ParseProviderDef parses raw YAML bytes into a ProviderDef. Exported wrapper
// around the package-internal parser so the CLI can validate user override
// files without re-implementing parsing.
func ParseProviderDef(data []byte) (ProviderDef, error) {
	return parseProviderDef(data)
}

// labelRE matches a single DNS label per RFC 1123: lowercase letters, digits,
// hyphens; no leading or trailing hyphen; 1-63 chars.
var labelRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// ValidateHostname returns an error if host is not a bare DNS hostname.
// Rejects schemes, paths, queries, ports, userinfo, single-label names, and
// labels that violate RFC 1123.
func ValidateHostname(host string) error {
	if host == "" {
		return fmt.Errorf("--host must be a bare hostname (e.g., gitlab.acme.com), got %q", host)
	}
	if len(host) > 253 {
		return fmt.Errorf("--host exceeds 253 chars: %q", host)
	}
	if strings.ContainsAny(host, ":/?#@") {
		return fmt.Errorf("--host must be a bare hostname (e.g., gitlab.acme.com), got %q", host)
	}
	if !strings.Contains(host, ".") {
		return fmt.Errorf("--host must include a domain (e.g., gitlab.acme.com), got %q", host)
	}
	for _, label := range strings.Split(host, ".") {
		if !labelRE.MatchString(label) {
			return fmt.Errorf("--host has invalid label %q (RFC 1123: lowercase letters, digits, hyphens; no leading/trailing hyphen; ≤1-63 chars)", label)
		}
	}
	return nil
}
