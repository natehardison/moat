package configprovider

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/majorcontext/moat/internal/config"
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
