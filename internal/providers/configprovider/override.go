package configprovider

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
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
