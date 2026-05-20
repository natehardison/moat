package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/majorcontext/moat/internal/config"
)

var (
	configShowSource     bool
	configShowWorkspace  string
	configShowNoDefaults bool
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Inspect moat configuration",
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Print the resolved moat config (project moat.yaml merged with ~/.moat/defaults.yaml)",
	Long: `Print the resolved moat config as YAML.

By default, the project moat.yaml is merged with ~/.moat/defaults.yaml (or
$MOAT_HOME/defaults.yaml if MOAT_HOME is set). Use --no-defaults to print
the project-only config without merging.

With --source, each line is annotated with a trailing comment showing where
that value came from: ` + "`# project`" + `, ` + "`# defaults`" + `, or ` + "`# merged`" + ` (slices/maps
where both sides contributed).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		workspace := configShowWorkspace
		if workspace == "" {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("getting working directory: %w", err)
			}
			workspace = cwd
		}
		return runConfigShow(os.Stdout, workspace, configShowSource, configShowNoDefaults)
	},
}

func init() {
	rootCmd.AddCommand(configCmd)
	configCmd.AddCommand(configShowCmd)
	configShowCmd.Flags().BoolVar(&configShowSource, "source", false,
		"Annotate each line with its source: project, defaults, or merged")
	configShowCmd.Flags().StringVar(&configShowWorkspace, "workspace", "",
		"Inspect a project at a non-cwd path")
	configShowCmd.Flags().BoolVar(&configShowNoDefaults, "no-defaults", false,
		"Print the project-only config without merging ~/.moat/defaults.yaml")
}

func runConfigShow(w io.Writer, workspace string, withSource, noDefaults bool) error {
	var cfg *config.Config
	var sources config.SourceMap
	if noDefaults {
		c, err := config.LoadProject(workspace)
		if err != nil {
			return err
		}
		cfg = c
		if cfg == nil {
			cfg = &config.Config{}
		}
	} else {
		project, err := config.LoadProject(workspace)
		if err != nil {
			return err
		}
		defaults, err := config.LoadDefaults()
		if err != nil {
			return err
		}
		cfg = config.MergeConfig(defaults, project)
		if withSource {
			sources = config.Sources(defaults, project, cfg)
		}
	}

	out, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	if !withSource {
		_, err = w.Write(out)
		return err
	}
	annotated := annotateYAML(string(out), sources)
	_, err = io.WriteString(w, annotated)
	return err
}

// annotateYAML appends `# <source>` comments to each YAML line whose dotted
// path is in `sources`. Lines without a source mapping pass through unchanged.
//
// This is a best-effort post-processor: it tracks indentation depth to derive
// a dotted key path for each scalar line and looks up that path in `sources`.
// Container keys (lines ending in just `:` with no inline value) are NOT
// annotated even if their path exists in `sources`. Slice/map element keys
// like `path[v]` and `path.key` are looked up when their value appears on a
// `- value` or `key: value` line.
func annotateYAML(yamlStr string, sources config.SourceMap) string {
	if len(sources) == 0 {
		return yamlStr
	}
	lines := strings.Split(yamlStr, "\n")
	var keyStack []string
	var indentStack []int
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " ")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(trimmed)
		// Pop stack to current indent.
		for len(indentStack) > 0 && indentStack[len(indentStack)-1] >= indent {
			keyStack = keyStack[:len(keyStack)-1]
			indentStack = indentStack[:len(indentStack)-1]
		}

		// Handle list items: "- value" form. Annotate using parent[value] key.
		if strings.HasPrefix(trimmed, "- ") {
			value := strings.TrimSpace(trimmed[2:])
			if len(keyStack) > 0 {
				path := strings.Join(keyStack, ".") + "[" + value + "]"
				if src, ok := sources[path]; ok && src != config.SourceUnset {
					lines[i] = line + "  # " + src.String()
				}
			}
			continue
		}

		// Parse "key:" or "key: value".
		// Note: continuation lines of block scalars (e.g. the lines under
		// "pre_run: |") that happen to contain a colon may be parsed as a
		// key here. If they push a spurious entry onto the stack, the pop
		// loop above removes it as soon as a real key at a lower indentation
		// is processed. Because spurious paths (e.g. "hooks.echo key") never
		// appear in sources, no incorrect annotation is emitted. The stack
		// is self-healing; block scalars do not require special handling.
		colon := strings.IndexByte(trimmed, ':')
		if colon < 0 {
			continue
		}
		key := trimmed[:colon]
		path := strings.Join(append(append([]string{}, keyStack...), key), ".")

		valueAfter := strings.TrimSpace(trimmed[colon+1:])
		if valueAfter != "" {
			// "key: value" — annotate if the path has a source.
			if src, ok := sources[path]; ok && src != config.SourceUnset {
				lines[i] = line + "  # " + src.String()
			}
		}
		// If this line is a container (no value), push it.
		if valueAfter == "" {
			keyStack = append(keyStack, key)
			indentStack = append(indentStack, indent)
		}
	}
	return strings.Join(lines, "\n")
}
