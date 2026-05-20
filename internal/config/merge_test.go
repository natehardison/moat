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
