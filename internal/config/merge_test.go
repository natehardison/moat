package config

import (
	"reflect"
	"testing"
)

func TestMergeConfig_Scalars(t *testing.T) {
	cases := []struct {
		name     string
		defaults *Config
		project  *Config
		want     func(*Config)
	}{
		{
			name:     "project agent wins",
			defaults: &Config{Agent: "claude"},
			project:  &Config{Agent: "codex"},
			want:     func(c *Config) { c.Agent = "codex" },
		},
		{
			name:     "defaults agent fills empty project",
			defaults: &Config{Agent: "claude"},
			project:  &Config{},
			want:     func(c *Config) { c.Agent = "claude" },
		},
		{
			name:     "project name wins; defaults runtime fills",
			defaults: &Config{Name: "default-name", Runtime: "docker"},
			project:  &Config{Name: "proj-name"},
			want:     func(c *Config) { c.Name = "proj-name"; c.Runtime = "docker" },
		},
		{
			name:     "interactive bool: project true wins over defaults false",
			defaults: &Config{Interactive: false},
			project:  &Config{Interactive: true},
			want:     func(c *Config) { c.Interactive = true },
		},
		{
			name:     "interactive bool: defaults true survives when project false (zero value)",
			defaults: &Config{Interactive: true},
			project:  &Config{Interactive: false},
			want:     func(c *Config) { c.Interactive = true },
		},
		{
			name:     "base_image and sandbox scalars",
			defaults: &Config{BaseImage: "debian:bookworm-slim", Sandbox: "none"},
			project:  &Config{BaseImage: "ubuntu:24.04"},
			want:     func(c *Config) { c.BaseImage = "ubuntu:24.04"; c.Sandbox = "none" },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MergeConfig(tc.defaults, tc.project)
			want := &Config{}
			tc.want(want)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("merged = %#v\nwant     %#v", got, want)
			}
		})
	}
}

func TestMergeConfig_Maps(t *testing.T) {
	defaults := &Config{
		Env:     map[string]string{"A": "1", "B": "2"},
		Secrets: map[string]string{"S1": "op://a"},
		Ports:   map[string]int{"http": 8080, "ws": 9000},
	}
	project := &Config{
		Env:     map[string]string{"B": "two", "C": "3"},
		Secrets: map[string]string{"S2": "op://b"},
		Ports:   map[string]int{"http": 8888},
	}
	got := MergeConfig(defaults, project)

	wantEnv := map[string]string{"A": "1", "B": "two", "C": "3"}
	if !reflect.DeepEqual(got.Env, wantEnv) {
		t.Errorf("Env = %v, want %v", got.Env, wantEnv)
	}
	wantSecrets := map[string]string{"S1": "op://a", "S2": "op://b"}
	if !reflect.DeepEqual(got.Secrets, wantSecrets) {
		t.Errorf("Secrets = %v, want %v", got.Secrets, wantSecrets)
	}
	wantPorts := map[string]int{"http": 8888, "ws": 9000}
	if !reflect.DeepEqual(got.Ports, wantPorts) {
		t.Errorf("Ports = %v, want %v", got.Ports, wantPorts)
	}
}

func TestMergeConfig_ServicesDeepCopy(t *testing.T) {
	// Verify the returned Config's Services entries do NOT alias the input's
	// internal maps. Mutating merged.Services["x"].Env must not affect defaults'.
	defaults := &Config{
		Services: map[string]ServiceSpec{
			"x": {Env: map[string]string{"KEY": "original"}},
		},
	}
	merged := MergeConfig(defaults, nil)

	// Confirm the value was copied correctly.
	if merged.Services["x"].Env["KEY"] != "original" {
		t.Fatalf("merged.Services[x].Env[KEY] = %q, want %q", merged.Services["x"].Env["KEY"], "original")
	}

	// Mutate the defaults' map and confirm the merged result is unaffected.
	defaults.Services["x"].Env["KEY"] = "mutated"
	if merged.Services["x"].Env["KEY"] != "original" {
		t.Errorf("merged.Services[x].Env aliases defaults: got %q after mutating defaults, want %q",
			merged.Services["x"].Env["KEY"], "original")
	}
}

func TestMergeConfig_StringSlices(t *testing.T) {
	cases := []struct {
		name             string
		defaults         *Config
		project          *Config
		wantDependencies []string
		wantGrants       []string
		wantCommand      []string
		wantLangServers  []string
	}{
		{
			name:             "union with dedupe",
			defaults:         &Config{Dependencies: []string{"node@22", "git"}, Grants: []string{"aws"}},
			project:          &Config{Dependencies: []string{"git", "go"}, Grants: []string{"github"}},
			wantDependencies: []string{"node@22", "git", "go"},
			wantGrants:       []string{"aws", "github"},
		},
		{
			name:        "project-only command (Command does NOT union — it's an invocation, not a list of independent items)",
			defaults:    &Config{Command: []string{"agent", "--default-flag"}},
			project:     &Config{Command: []string{"agent", "--project-flag"}},
			wantCommand: []string{"agent", "--project-flag"},
		},
		{
			name:        "command fills from defaults when project unset",
			defaults:    &Config{Command: []string{"agent", "--default-flag"}},
			project:     &Config{},
			wantCommand: []string{"agent", "--default-flag"},
		},
		{
			name:            "language_servers union",
			defaults:        &Config{LanguageServers: []string{"gopls"}},
			project:         &Config{LanguageServers: []string{"typescript-language-server"}},
			wantLangServers: []string{"gopls", "typescript-language-server"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MergeConfig(tc.defaults, tc.project)
			if !reflect.DeepEqual(got.Dependencies, tc.wantDependencies) {
				t.Errorf("Dependencies = %v, want %v", got.Dependencies, tc.wantDependencies)
			}
			if !reflect.DeepEqual(got.Grants, tc.wantGrants) {
				t.Errorf("Grants = %v, want %v", got.Grants, tc.wantGrants)
			}
			if !reflect.DeepEqual(got.Command, tc.wantCommand) {
				t.Errorf("Command = %v, want %v", got.Command, tc.wantCommand)
			}
			if !reflect.DeepEqual(got.LanguageServers, tc.wantLangServers) {
				t.Errorf("LanguageServers = %v, want %v", got.LanguageServers, tc.wantLangServers)
			}
		})
	}
}

func TestMergeConfig_StructSlices(t *testing.T) {
	t.Run("Mounts keyed by (Source,Target); project wins on collision", func(t *testing.T) {
		defaults := &Config{Mounts: []MountEntry{
			{Source: "/host/a", Target: "/c/a"},
			{Source: "/host/b", Target: "/c/b", ReadOnly: true},
		}}
		project := &Config{Mounts: []MountEntry{
			{Source: "/host/b", Target: "/c/b", ReadOnly: false}, // collision, project wins
			{Source: "/host/c", Target: "/c/c"},                  // new
		}}
		got := MergeConfig(defaults, project)
		want := []MountEntry{
			{Source: "/host/a", Target: "/c/a"},
			{Source: "/host/b", Target: "/c/b", ReadOnly: false},
			{Source: "/host/c", Target: "/c/c"},
		}
		if !reflect.DeepEqual(got.Mounts, want) {
			t.Errorf("Mounts = %+v\nwant      %+v", got.Mounts, want)
		}
	})

	t.Run("Mounts same source, different targets, both kept", func(t *testing.T) {
		defaults := &Config{Mounts: []MountEntry{{Source: "/host/a", Target: "/c/a"}}}
		project := &Config{Mounts: []MountEntry{{Source: "/host/a", Target: "/c/different"}}}
		got := MergeConfig(defaults, project)
		if len(got.Mounts) != 2 {
			t.Errorf("expected both mounts retained, got %+v", got.Mounts)
		}
	})

	t.Run("Volumes keyed by Name", func(t *testing.T) {
		defaults := &Config{Volumes: []VolumeConfig{{Name: "cache", Target: "/cache"}}}
		project := &Config{Volumes: []VolumeConfig{
			{Name: "cache", Target: "/cache", ReadOnly: true}, // collision, project wins
			{Name: "data", Target: "/data"},                   // new
		}}
		got := MergeConfig(defaults, project)
		want := []VolumeConfig{
			{Name: "cache", Target: "/cache", ReadOnly: true},
			{Name: "data", Target: "/data"},
		}
		if !reflect.DeepEqual(got.Volumes, want) {
			t.Errorf("Volumes = %+v\nwant      %+v", got.Volumes, want)
		}
	})

	t.Run("MCP keyed by Name", func(t *testing.T) {
		defaults := &Config{MCP: []MCPServerConfig{{Name: "filesys", URL: "https://a"}}}
		project := &Config{MCP: []MCPServerConfig{
			{Name: "filesys", URL: "https://b"}, // collision, project wins
			{Name: "github", URL: "https://gh"}, // new
		}}
		got := MergeConfig(defaults, project)
		if len(got.MCP) != 2 {
			t.Fatalf("MCP len = %d, want 2", len(got.MCP))
		}
		if got.MCP[0].Name != "filesys" || got.MCP[0].URL != "https://b" {
			t.Errorf("MCP[0] = %+v, want {filesys, https://b}", got.MCP[0])
		}
		if got.MCP[1].Name != "github" {
			t.Errorf("MCP[1] = %+v, want github entry", got.MCP[1])
		}
	})
}

func TestMergeConfig_NilInputs(t *testing.T) {
	t.Run("both nil returns empty Config", func(t *testing.T) {
		got := MergeConfig(nil, nil)
		if got == nil {
			t.Fatal("MergeConfig(nil, nil) = nil, want non-nil empty")
		}
		if !reflect.DeepEqual(got, &Config{}) {
			t.Errorf("got = %#v, want &Config{}", got)
		}
	})
	t.Run("nil defaults returns clone of project", func(t *testing.T) {
		project := &Config{Agent: "claude", Env: map[string]string{"X": "1"}}
		got := MergeConfig(nil, project)
		if !reflect.DeepEqual(got, project) {
			t.Errorf("got = %#v, want %#v", got, project)
		}
		// Confirm clone, not alias.
		project.Env["X"] = "MUTATED"
		if got.Env["X"] != "1" {
			t.Errorf("got.Env aliases project.Env: %v", got.Env)
		}
	})
	t.Run("nil project returns clone of defaults", func(t *testing.T) {
		defaults := &Config{Agent: "claude", Env: map[string]string{"X": "1"}}
		got := MergeConfig(defaults, nil)
		if !reflect.DeepEqual(got, defaults) {
			t.Errorf("got = %#v, want %#v", got, defaults)
		}
	})
}
