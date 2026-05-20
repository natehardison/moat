package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// DefaultsFilename is the per-user defaults file name.
// Located at <GlobalConfigDir>/defaults.yaml (default ~/.moat/defaults.yaml,
// or $MOAT_HOME/defaults.yaml when MOAT_HOME is set).
const DefaultsFilename = "defaults.yaml"

// LoadDefaults reads the per-user moat.yaml defaults file, if it exists.
//
// Returns:
//   - (cfg, nil) when the file exists and parses cleanly.
//   - (nil, nil) when the file does not exist — this is the common case for
//     users who do not use defaults.
//   - (nil, err) when the file exists but cannot be read or parsed.
//
// The file's schema is identical to moat.yaml; missing fields are zero values
// and are filled in by the project moat.yaml during MergeConfig.
func LoadDefaults() (*Config, error) {
	path := filepath.Join(GlobalConfigDir(), DefaultsFilename)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &cfg, nil
}
