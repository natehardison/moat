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

func TestMergeConfig_Claude(t *testing.T) {
	t.Run("plugins map per-key merge", func(t *testing.T) {
		defaults := &Config{Claude: ClaudeConfig{Plugins: map[string]bool{"a@m": true, "b@m": true}}}
		project := &Config{Claude: ClaudeConfig{Plugins: map[string]bool{"b@m": false, "c@m": true}}}
		got := MergeConfig(defaults, project)
		want := map[string]bool{"a@m": true, "b@m": false, "c@m": true}
		if !reflect.DeepEqual(got.Claude.Plugins, want) {
			t.Errorf("Plugins = %v, want %v", got.Claude.Plugins, want)
		}
	})

	t.Run("base_url scalar fill from defaults", func(t *testing.T) {
		defaults := &Config{Claude: ClaudeConfig{BaseURL: "https://default.test"}}
		project := &Config{Claude: ClaudeConfig{}}
		got := MergeConfig(defaults, project)
		if got.Claude.BaseURL != "https://default.test" {
			t.Errorf("Claude.BaseURL = %q, want https://default.test", got.Claude.BaseURL)
		}
	})

	t.Run("base_url project wins", func(t *testing.T) {
		defaults := &Config{Claude: ClaudeConfig{BaseURL: "https://default.test"}}
		project := &Config{Claude: ClaudeConfig{BaseURL: "https://project.test"}}
		got := MergeConfig(defaults, project)
		if got.Claude.BaseURL != "https://project.test" {
			t.Errorf("Claude.BaseURL = %q, want https://project.test", got.Claude.BaseURL)
		}
	})

	t.Run("sync_logs boolptr: defaults fills nil project", func(t *testing.T) {
		tru := true
		defaults := &Config{Claude: ClaudeConfig{SyncLogs: &tru}}
		project := &Config{}
		got := MergeConfig(defaults, project)
		if got.Claude.SyncLogs == nil || !*got.Claude.SyncLogs {
			t.Errorf("Claude.SyncLogs = %v, want pointer to true", got.Claude.SyncLogs)
		}
	})

	t.Run("sync_logs boolptr: project false wins over defaults true", func(t *testing.T) {
		tru := true
		fls := false
		defaults := &Config{Claude: ClaudeConfig{SyncLogs: &tru}}
		project := &Config{Claude: ClaudeConfig{SyncLogs: &fls}}
		got := MergeConfig(defaults, project)
		if got.Claude.SyncLogs == nil || *got.Claude.SyncLogs {
			t.Errorf("Claude.SyncLogs = %v, want pointer to false", got.Claude.SyncLogs)
		}
	})

	t.Run("marketplaces per-key merge", func(t *testing.T) {
		defaults := &Config{Claude: ClaudeConfig{Marketplaces: map[string]MarketplaceSpec{
			"corp": {Source: "github", Repo: "corp/plugins"},
		}}}
		project := &Config{Claude: ClaudeConfig{Marketplaces: map[string]MarketplaceSpec{
			"local": {Source: "directory", Path: "/my/plugins"},
		}}}
		got := MergeConfig(defaults, project)
		if len(got.Claude.Marketplaces) != 2 {
			t.Fatalf("Marketplaces len = %d, want 2", len(got.Claude.Marketplaces))
		}
		if _, ok := got.Claude.Marketplaces["corp"]; !ok {
			t.Error("missing 'corp' marketplace from defaults")
		}
		if _, ok := got.Claude.Marketplaces["local"]; !ok {
			t.Error("missing 'local' marketplace from project")
		}
	})

	t.Run("marketplaces project wins on key collision", func(t *testing.T) {
		defaults := &Config{Claude: ClaudeConfig{Marketplaces: map[string]MarketplaceSpec{
			"corp": {Source: "github", Repo: "corp/old"},
		}}}
		project := &Config{Claude: ClaudeConfig{Marketplaces: map[string]MarketplaceSpec{
			"corp": {Source: "github", Repo: "corp/new"},
		}}}
		got := MergeConfig(defaults, project)
		if got.Claude.Marketplaces["corp"].Repo != "corp/new" {
			t.Errorf("Marketplaces[corp].Repo = %q, want corp/new", got.Claude.Marketplaces["corp"].Repo)
		}
	})

	t.Run("claude mcp map per-key merge", func(t *testing.T) {
		defaults := &Config{Claude: ClaudeConfig{MCP: map[string]MCPServerSpec{
			"server-a": {Command: "npx", Args: []string{"-y", "mcp-a"}},
		}}}
		project := &Config{Claude: ClaudeConfig{MCP: map[string]MCPServerSpec{
			"server-b": {Command: "npx", Args: []string{"-y", "mcp-b"}},
		}}}
		got := MergeConfig(defaults, project)
		if len(got.Claude.MCP) != 2 {
			t.Fatalf("Claude.MCP len = %d, want 2", len(got.Claude.MCP))
		}
	})

	t.Run("llm_gateway pointer: defaults fills nil project", func(t *testing.T) {
		defaults := &Config{Claude: ClaudeConfig{LLMGateway: &LLMGatewayConfig{}}}
		project := &Config{}
		got := MergeConfig(defaults, project)
		if got.Claude.LLMGateway == nil {
			t.Error("Claude.LLMGateway = nil, want non-nil from defaults")
		}
	})

	t.Run("llm_gateway pointer: project wins if set", func(t *testing.T) {
		projGW := &LLMGatewayConfig{}
		defaults := &Config{Claude: ClaudeConfig{LLMGateway: &LLMGatewayConfig{}}}
		project := &Config{Claude: ClaudeConfig{LLMGateway: projGW}}
		got := MergeConfig(defaults, project)
		if got.Claude.LLMGateway == nil {
			t.Error("Claude.LLMGateway = nil, want non-nil from project")
		}
	})
}

func TestMergeConfig_Container(t *testing.T) {
	defaults := &Config{Container: ContainerConfig{Memory: 4096, CPUs: 4, DNS: []string{"8.8.8.8"}}}
	project := &Config{Container: ContainerConfig{Memory: 8192}}
	got := MergeConfig(defaults, project)
	if got.Container.Memory != 8192 {
		t.Errorf("Memory = %d, want 8192 (project)", got.Container.Memory)
	}
	if got.Container.CPUs != 4 {
		t.Errorf("CPUs = %d, want 4 (defaults)", got.Container.CPUs)
	}
	if !reflect.DeepEqual(got.Container.DNS, []string{"8.8.8.8"}) {
		t.Errorf("DNS = %v, want [8.8.8.8] (defaults)", got.Container.DNS)
	}

	t.Run("project dns replaces defaults dns", func(t *testing.T) {
		d := &Config{Container: ContainerConfig{DNS: []string{"8.8.8.8"}}}
		p := &Config{Container: ContainerConfig{DNS: []string{"1.1.1.1"}}}
		got := MergeConfig(d, p)
		if !reflect.DeepEqual(got.Container.DNS, []string{"1.1.1.1"}) {
			t.Errorf("DNS = %v, want [1.1.1.1]", got.Container.DNS)
		}
	})

	t.Run("ulimits per-key merge", func(t *testing.T) {
		d := &Config{Container: ContainerConfig{Ulimits: map[string]UlimitSpec{
			"nofile": {Soft: 1024, Hard: 65536},
		}}}
		p := &Config{Container: ContainerConfig{Ulimits: map[string]UlimitSpec{
			"nproc": {Soft: 256, Hard: 1024},
		}}}
		got := MergeConfig(d, p)
		if len(got.Container.Ulimits) != 2 {
			t.Fatalf("Ulimits len = %d, want 2", len(got.Container.Ulimits))
		}
	})
}

func TestMergeConfig_Network(t *testing.T) {
	defaults := &Config{Network: NetworkConfig{Policy: "strict"}}
	project := &Config{}
	got := MergeConfig(defaults, project)
	if got.Network.Policy != "strict" {
		t.Errorf("Policy = %q, want strict (from defaults)", got.Network.Policy)
	}

	t.Run("project policy wins", func(t *testing.T) {
		d := &Config{Network: NetworkConfig{Policy: "strict"}}
		p := &Config{Network: NetworkConfig{Policy: "permissive"}}
		got := MergeConfig(d, p)
		if got.Network.Policy != "permissive" {
			t.Errorf("Policy = %q, want permissive", got.Network.Policy)
		}
	})

	t.Run("host ports: project replaces defaults", func(t *testing.T) {
		d := &Config{Network: NetworkConfig{Host: []int{8080}}}
		p := &Config{Network: NetworkConfig{Host: []int{9090}}}
		got := MergeConfig(d, p)
		if !reflect.DeepEqual(got.Network.Host, []int{9090}) {
			t.Errorf("Host = %v, want [9090]", got.Network.Host)
		}
	})

	t.Run("host ports: defaults fills empty project", func(t *testing.T) {
		d := &Config{Network: NetworkConfig{Host: []int{8080}}}
		p := &Config{}
		got := MergeConfig(d, p)
		if !reflect.DeepEqual(got.Network.Host, []int{8080}) {
			t.Errorf("Host = %v, want [8080]", got.Network.Host)
		}
	})
}

func TestMergeConfig_Codex(t *testing.T) {
	t.Run("sync_logs boolptr: defaults fills nil project", func(t *testing.T) {
		tru := true
		defaults := &Config{Codex: CodexConfig{SyncLogs: &tru}}
		project := &Config{}
		got := MergeConfig(defaults, project)
		if got.Codex.SyncLogs == nil || !*got.Codex.SyncLogs {
			t.Errorf("Codex.SyncLogs = %v, want pointer to true", got.Codex.SyncLogs)
		}
	})

	t.Run("mcp per-key merge", func(t *testing.T) {
		defaults := &Config{Codex: CodexConfig{MCP: map[string]MCPServerSpec{
			"a": {Command: "cmd-a"},
		}}}
		project := &Config{Codex: CodexConfig{MCP: map[string]MCPServerSpec{
			"b": {Command: "cmd-b"},
		}}}
		got := MergeConfig(defaults, project)
		if len(got.Codex.MCP) != 2 {
			t.Fatalf("Codex.MCP len = %d, want 2", len(got.Codex.MCP))
		}
	})
}

func TestMergeConfig_Gemini(t *testing.T) {
	t.Run("sync_logs boolptr: defaults fills nil project", func(t *testing.T) {
		tru := true
		defaults := &Config{Gemini: GeminiConfig{SyncLogs: &tru}}
		project := &Config{}
		got := MergeConfig(defaults, project)
		if got.Gemini.SyncLogs == nil || !*got.Gemini.SyncLogs {
			t.Errorf("Gemini.SyncLogs = %v, want pointer to true", got.Gemini.SyncLogs)
		}
	})
}

func TestMergeConfig_Snapshots(t *testing.T) {
	t.Run("disabled bool: project true wins", func(t *testing.T) {
		defaults := &Config{Snapshots: SnapshotConfig{Disabled: false}}
		project := &Config{Snapshots: SnapshotConfig{Disabled: true}}
		got := MergeConfig(defaults, project)
		if !got.Snapshots.Disabled {
			t.Error("Snapshots.Disabled = false, want true (project)")
		}
	})

	t.Run("triggers: idle threshold fills from defaults", func(t *testing.T) {
		defaults := &Config{Snapshots: SnapshotConfig{
			Triggers: SnapshotTriggerConfig{IdleThresholdSeconds: 60},
		}}
		project := &Config{}
		got := MergeConfig(defaults, project)
		if got.Snapshots.Triggers.IdleThresholdSeconds != 60 {
			t.Errorf("IdleThresholdSeconds = %d, want 60", got.Snapshots.Triggers.IdleThresholdSeconds)
		}
	})

	t.Run("exclude additional slice: project replaces defaults", func(t *testing.T) {
		defaults := &Config{Snapshots: SnapshotConfig{
			Exclude: SnapshotExcludeConfig{Additional: []string{"*.log"}},
		}}
		project := &Config{Snapshots: SnapshotConfig{
			Exclude: SnapshotExcludeConfig{Additional: []string{"*.tmp"}},
		}}
		got := MergeConfig(defaults, project)
		if !reflect.DeepEqual(got.Snapshots.Exclude.Additional, []string{"*.tmp"}) {
			t.Errorf("Exclude.Additional = %v, want [*.tmp]", got.Snapshots.Exclude.Additional)
		}
	})

	t.Run("retention max_count: project wins", func(t *testing.T) {
		defaults := &Config{Snapshots: SnapshotConfig{
			Retention: SnapshotRetentionConfig{MaxCount: 10},
		}}
		project := &Config{Snapshots: SnapshotConfig{
			Retention: SnapshotRetentionConfig{MaxCount: 20},
		}}
		got := MergeConfig(defaults, project)
		if got.Snapshots.Retention.MaxCount != 20 {
			t.Errorf("Retention.MaxCount = %d, want 20", got.Snapshots.Retention.MaxCount)
		}
	})
}

func TestMergeConfig_Hooks(t *testing.T) {
	t.Run("pre_run: project wins over defaults", func(t *testing.T) {
		defaults := &Config{Hooks: HooksConfig{PreRun: "echo defaults"}}
		project := &Config{Hooks: HooksConfig{PreRun: "echo project"}}
		got := MergeConfig(defaults, project)
		if got.Hooks.PreRun != "echo project" {
			t.Errorf("Hooks.PreRun = %q, want 'echo project'", got.Hooks.PreRun)
		}
	})

	t.Run("post_build: defaults fills empty project", func(t *testing.T) {
		defaults := &Config{Hooks: HooksConfig{PostBuild: "echo post-build"}}
		project := &Config{}
		got := MergeConfig(defaults, project)
		if got.Hooks.PostBuild != "echo post-build" {
			t.Errorf("Hooks.PostBuild = %q, want 'echo post-build'", got.Hooks.PostBuild)
		}
	})
}

func TestMergeConfig_Tracing(t *testing.T) {
	t.Run("disable_exec: project true wins", func(t *testing.T) {
		defaults := &Config{Tracing: TracingConfig{DisableExec: false}}
		project := &Config{Tracing: TracingConfig{DisableExec: true}}
		got := MergeConfig(defaults, project)
		if !got.Tracing.DisableExec {
			t.Error("Tracing.DisableExec = false, want true (project)")
		}
	})

	t.Run("disable_exec: defaults true survives when project zero", func(t *testing.T) {
		defaults := &Config{Tracing: TracingConfig{DisableExec: true}}
		project := &Config{}
		got := MergeConfig(defaults, project)
		if !got.Tracing.DisableExec {
			t.Error("Tracing.DisableExec = false, want true (defaults)")
		}
	})
}

func TestMergeConfig_Clipboard(t *testing.T) {
	tru := true
	fls := false
	t.Run("project nil → defaults wins", func(t *testing.T) {
		got := MergeConfig(&Config{Clipboard: &fls}, &Config{})
		if got.Clipboard == nil || *got.Clipboard != false {
			t.Errorf("got %+v, want pointer to false", got.Clipboard)
		}
	})
	t.Run("project set → project wins", func(t *testing.T) {
		got := MergeConfig(&Config{Clipboard: &fls}, &Config{Clipboard: &tru})
		if got.Clipboard == nil || *got.Clipboard != true {
			t.Errorf("got %+v, want pointer to true", got.Clipboard)
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
