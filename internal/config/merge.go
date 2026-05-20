package config

import (
	"github.com/majorcontext/moat/internal/keep"
	"github.com/majorcontext/moat/internal/netrules"
)

// MergeConfig returns the resolved Config produced by merging defaults under
// project: project values win per field when set; defaults fill in missing
// (zero-value) fields; maps merge per-key with project winning per key;
// slices union with project entries appended after defaults (deduped per
// element shape — see Task 3 and Task 4 for slice rules).
//
// Either argument may be nil. MergeConfig never mutates its arguments and
// never returns nil. It is pure with respect to time, environment, and the
// filesystem.
//
// This file is hand-maintained per-field. Adding a new field to Config
// requires extending MergeConfig to cover it. The reflection-guarded
// TestMergeConfigCoversAllFields test in merge_test.go fails when a new
// field is added without merge support (see Task 6).
func MergeConfig(defaults, project *Config) *Config {
	if defaults == nil && project == nil {
		return &Config{}
	}
	if defaults == nil {
		return cloneConfig(project)
	}
	if project == nil {
		return cloneConfig(defaults)
	}

	out := &Config{}
	mergeScalars(defaults, project, out)
	mergeMaps(defaults, project, out)
	mergeSlices(defaults, project, out)
	mergeNested(defaults, project, out)
	return out
}

// mergeScalars handles scalar (and scalar-pointer) fields on Config.
// Rule: project wins if non-zero; defaults fills in otherwise. Bool fields
// use "OR semantics" — true survives from either side, because a project
// explicitly setting `interactive: false` is indistinguishable from omitting
// the field in the zero-value YAML decoding.
func mergeScalars(d, p, out *Config) {
	out.Name = pickStr(p.Name, d.Name)
	out.Agent = pickStr(p.Agent, d.Agent)
	out.Version = pickStr(p.Version, d.Version)
	out.Interactive = p.Interactive || d.Interactive
	out.Sandbox = pickStr(p.Sandbox, d.Sandbox)
	out.Runtime = pickStr(p.Runtime, d.Runtime)
	out.BaseImage = pickStr(p.BaseImage, d.BaseImage)
}

// mergeMaps handles map fields on Config. Per-key merge; project wins per
// key. Nil maps are treated as empty.
func mergeMaps(d, p, out *Config) {
	out.Env = mergeStringMap(d.Env, p.Env)
	out.Secrets = mergeStringMap(d.Secrets, p.Secrets)
	out.Ports = mergeIntMap(d.Ports, p.Ports)
	out.Services = mergeServicesMap(d.Services, p.Services)
}

// cloneConfig returns a deep copy of c that can be mutated without affecting
// the original. It never returns nil.
//
// Unlike MergeConfig, this function preserves slice duplicates (e.g. two
// volumes with the same name) so that validateConfig can still detect them.
// Using mergeSlices here would deduplicate the slices as a side-effect of
// merge logic, which would mask invalid configs during pre-validation.
func cloneConfig(c *Config) *Config {
	if c == nil {
		return &Config{}
	}
	out := &Config{}

	// Scalars and scalar-like nested structs (all value types — no aliasing).
	mergeScalars(&Config{}, c, out)
	out.Interactive = c.Interactive
	out.Container = mergeContainerConfig(ContainerConfig{}, c.Container)
	out.Network = mergeNetworkConfig(NetworkConfig{}, c.Network)
	// mergeNetworkConfig deliberately drops Network.Allow (deprecated hard-error
	// field). Preserve it in the clone so pre-validation can still catch it.
	if c.Network.Allow != nil {
		out.Network.Allow = append([]string(nil), c.Network.Allow...)
	}
	out.Snapshots = mergeSnapshotConfig(SnapshotConfig{}, c.Snapshots)
	out.Tracing = TracingConfig{DisableExec: c.Tracing.DisableExec}
	out.Hooks = HooksConfig{
		PostBuild:     c.Hooks.PostBuild,
		PostBuildRoot: c.Hooks.PostBuildRoot,
		PreRun:        c.Hooks.PreRun,
	}
	out.Clipboard = mergeBoolPtr(c.Clipboard, nil)

	// Maps — deep copy without deduplication.
	out.Env = mergeStringMap(nil, c.Env)
	out.Secrets = mergeStringMap(nil, c.Secrets)
	out.Ports = mergeIntMap(nil, c.Ports)
	out.Services = mergeServicesMap(nil, c.Services)

	// Nested agent configs — deep copy.
	out.Claude = mergeClaudeConfig(ClaudeConfig{}, c.Claude)
	out.Codex = mergeCodexConfig(CodexConfig{}, c.Codex)
	out.Gemini = mergeGeminiConfig(GeminiConfig{}, c.Gemini)

	// Slices — copy element-by-element WITHOUT deduplication so that
	// invalid configs (e.g. duplicate volume names) remain detectable.
	if c.Dependencies != nil {
		out.Dependencies = append([]string(nil), c.Dependencies...)
	}
	if c.Grants != nil {
		out.Grants = append([]string(nil), c.Grants...)
	}
	if c.LanguageServers != nil {
		out.LanguageServers = append([]string(nil), c.LanguageServers...)
	}
	if c.Command != nil {
		out.Command = append([]string(nil), c.Command...)
	}
	if c.Mounts != nil {
		out.Mounts = make([]MountEntry, len(c.Mounts))
		for i, m := range c.Mounts {
			out.Mounts[i] = cloneMountEntry(m)
		}
	}
	if c.Volumes != nil {
		out.Volumes = make([]VolumeConfig, len(c.Volumes))
		copy(out.Volumes, c.Volumes)
	}
	if c.MCP != nil {
		out.MCP = make([]MCPServerConfig, len(c.MCP))
		for i, m := range c.MCP {
			out.MCP[i] = cloneMCPServerConfig(m)
		}
	}

	return out
}

// mergeNested handles all nested-struct fields on Config. Each sub-struct
// gets its own merge function that applies the standard rules recursively.
func mergeNested(d, p, out *Config) {
	out.Claude = mergeClaudeConfig(d.Claude, p.Claude)
	out.Codex = mergeCodexConfig(d.Codex, p.Codex)
	out.Gemini = mergeGeminiConfig(d.Gemini, p.Gemini)
	out.Container = mergeContainerConfig(d.Container, p.Container)
	out.Network = mergeNetworkConfig(d.Network, p.Network)
	out.Snapshots = mergeSnapshotConfig(d.Snapshots, p.Snapshots)
	out.Tracing = mergeTracingConfig(d.Tracing, p.Tracing)
	out.Hooks = mergeHooksConfig(d.Hooks, p.Hooks)
	out.Clipboard = mergeBoolPtr(p.Clipboard, d.Clipboard)
}

// mergeClaudeConfig merges two ClaudeConfig values.
//
// Fields merged:
//   - BaseURL string — pickStr (project wins if non-empty)
//   - SyncLogs *bool — mergeBoolPtr (project wins if non-nil)
//   - Plugins map[string]bool — per-key merge; project wins per key
//   - Marketplaces map[string]MarketplaceSpec — per-key; project wins per key
//   - MCP map[string]MCPServerSpec — per-key; project wins per key
//   - LLMGateway *LLMGatewayConfig — opaque pointer; project wins if non-nil; both non-nil: recurse
//   - SkipPermissionsPrompt bool — yaml:"-"; OR semantics (true survives)
func mergeClaudeConfig(d, p ClaudeConfig) ClaudeConfig {
	return ClaudeConfig{
		BaseURL:               pickStr(p.BaseURL, d.BaseURL),
		SyncLogs:              mergeBoolPtr(p.SyncLogs, d.SyncLogs),
		Plugins:               mergeBoolMap(d.Plugins, p.Plugins),
		Marketplaces:          mergeMarketplaceMap(d.Marketplaces, p.Marketplaces),
		MCP:                   mergeMCPSpecMap(d.MCP, p.MCP),
		LLMGateway:            mergeLLMGatewayPtr(p.LLMGateway, d.LLMGateway),
		SkipPermissionsPrompt: p.SkipPermissionsPrompt || d.SkipPermissionsPrompt,
	}
}

// mergeCodexConfig merges two CodexConfig values.
//
// Fields merged:
//   - SyncLogs *bool — mergeBoolPtr
//   - MCP map[string]MCPServerSpec — per-key merge
func mergeCodexConfig(d, p CodexConfig) CodexConfig {
	return CodexConfig{
		SyncLogs: mergeBoolPtr(p.SyncLogs, d.SyncLogs),
		MCP:      mergeMCPSpecMap(d.MCP, p.MCP),
	}
}

// mergeGeminiConfig merges two GeminiConfig values.
//
// Fields merged:
//   - SyncLogs *bool — mergeBoolPtr
//   - MCP map[string]MCPServerSpec — per-key merge
func mergeGeminiConfig(d, p GeminiConfig) GeminiConfig {
	return GeminiConfig{
		SyncLogs: mergeBoolPtr(p.SyncLogs, d.SyncLogs),
		MCP:      mergeMCPSpecMap(d.MCP, p.MCP),
	}
}

// mergeContainerConfig merges two ContainerConfig values.
//
// Fields merged:
//   - Memory int — pickInt (project wins if non-zero)
//   - CPUs int — pickInt (project wins if non-zero)
//   - DNS []string — pickStrSlice (project replaces defaults; DNS order matters)
//   - Ulimits map[string]UlimitSpec — per-key; project wins per key
func mergeContainerConfig(d, p ContainerConfig) ContainerConfig {
	return ContainerConfig{
		Memory:  pickInt(p.Memory, d.Memory),
		CPUs:    pickInt(p.CPUs, d.CPUs),
		DNS:     pickStrSlice(p.DNS, d.DNS),
		Ulimits: mergeUlimitMap(d.Ulimits, p.Ulimits),
	}
}

// mergeNetworkConfig merges two NetworkConfig values.
//
// Fields merged:
//   - Policy string — pickStr (project wins if non-empty)
//   - Allow []string — deprecated; always left empty (hard error at parse time)
//   - Rules []NetworkRuleEntry — keyed by Host; project wins per host
//   - KeepPolicy *keep.PolicyConfig — opaque pointer; project wins if non-nil
//   - Host []int — pickIntSlice (project replaces defaults; order matters)
func mergeNetworkConfig(d, p NetworkConfig) NetworkConfig {
	return NetworkConfig{
		Policy:     pickStr(p.Policy, d.Policy),
		Rules:      mergeNetworkRules(d.Rules, p.Rules),
		KeepPolicy: mergePolicyConfigPtr(p.KeepPolicy, d.KeepPolicy),
		Host:       pickIntSlice(p.Host, d.Host),
	}
}

// mergeSnapshotConfig merges two SnapshotConfig values.
//
// Fields merged:
//   - Disabled bool — OR semantics (true survives from either side)
//   - Triggers SnapshotTriggerConfig — recurse
//   - Exclude SnapshotExcludeConfig — recurse
//   - Retention SnapshotRetentionConfig — recurse
func mergeSnapshotConfig(d, p SnapshotConfig) SnapshotConfig {
	return SnapshotConfig{
		Disabled:  p.Disabled || d.Disabled,
		Triggers:  mergeSnapshotTriggerConfig(d.Triggers, p.Triggers),
		Exclude:   mergeSnapshotExcludeConfig(d.Exclude, p.Exclude),
		Retention: mergeSnapshotRetentionConfig(d.Retention, p.Retention),
	}
}

// mergeSnapshotTriggerConfig merges two SnapshotTriggerConfig values.
//
// Fields merged (all bool with OR semantics, int with pickInt):
//   - DisablePreRun, DisableGitCommits, DisableBuilds, DisableIdle — OR
//   - IdleThresholdSeconds int — pickInt
func mergeSnapshotTriggerConfig(d, p SnapshotTriggerConfig) SnapshotTriggerConfig {
	return SnapshotTriggerConfig{
		DisablePreRun:        p.DisablePreRun || d.DisablePreRun,
		DisableGitCommits:    p.DisableGitCommits || d.DisableGitCommits,
		DisableBuilds:        p.DisableBuilds || d.DisableBuilds,
		DisableIdle:          p.DisableIdle || d.DisableIdle,
		IdleThresholdSeconds: pickInt(p.IdleThresholdSeconds, d.IdleThresholdSeconds),
	}
}

// mergeSnapshotExcludeConfig merges two SnapshotExcludeConfig values.
//
// Fields merged:
//   - IgnoreGitignore bool — OR semantics
//   - Additional []string — pickStrSlice (project replaces defaults)
func mergeSnapshotExcludeConfig(d, p SnapshotExcludeConfig) SnapshotExcludeConfig {
	return SnapshotExcludeConfig{
		IgnoreGitignore: p.IgnoreGitignore || d.IgnoreGitignore,
		Additional:      pickStrSlice(p.Additional, d.Additional),
	}
}

// mergeSnapshotRetentionConfig merges two SnapshotRetentionConfig values.
//
// Fields merged:
//   - MaxCount int — pickInt (project wins if non-zero)
//   - DeleteInitial bool — OR semantics
func mergeSnapshotRetentionConfig(d, p SnapshotRetentionConfig) SnapshotRetentionConfig {
	return SnapshotRetentionConfig{
		MaxCount:      pickInt(p.MaxCount, d.MaxCount),
		DeleteInitial: p.DeleteInitial || d.DeleteInitial,
	}
}

// mergeTracingConfig merges two TracingConfig values.
//
// Fields merged:
//   - DisableExec bool — OR semantics (true survives)
func mergeTracingConfig(d, p TracingConfig) TracingConfig {
	return TracingConfig{
		DisableExec: p.DisableExec || d.DisableExec,
	}
}

// mergeHooksConfig merges two HooksConfig values.
//
// Fields merged (all string with pickStr — project wins if non-empty):
//   - PostBuild string
//   - PostBuildRoot string
//   - PreRun string
func mergeHooksConfig(d, p HooksConfig) HooksConfig {
	return HooksConfig{
		PostBuild:     pickStr(p.PostBuild, d.PostBuild),
		PostBuildRoot: pickStr(p.PostBuildRoot, d.PostBuildRoot),
		PreRun:        pickStr(p.PreRun, d.PreRun),
	}
}

// mergeBoolPtr returns a fresh copy of primary if non-nil, else a fresh copy
// of fallback. Returns nil if both are nil.
func mergeBoolPtr(primary, fallback *bool) *bool {
	if primary != nil {
		b := *primary
		return &b
	}
	if fallback == nil {
		return nil
	}
	b := *fallback
	return &b
}

// pickInt returns primary if non-zero, otherwise fallback.
func pickInt(primary, fallback int) int {
	if primary != 0 {
		return primary
	}
	return fallback
}

// pickIntSlice returns primary if non-empty, else fallback (no merge).
// Returns nil iff both inputs are nil-or-empty.
func pickIntSlice(primary, fallback []int) []int {
	if len(primary) > 0 {
		out := make([]int, len(primary))
		copy(out, primary)
		return out
	}
	if len(fallback) == 0 {
		return nil
	}
	out := make([]int, len(fallback))
	copy(out, fallback)
	return out
}

// mergeBoolMap merges two map[string]bool maps per-key; override wins per key.
// Returns nil iff both inputs are nil-or-empty.
func mergeBoolMap(base, override map[string]bool) map[string]bool {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := make(map[string]bool, len(base)+len(override))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range override {
		out[k] = v
	}
	return out
}

// mergeMarketplaceMap merges two map[string]MarketplaceSpec maps per-key;
// override wins per key. MarketplaceSpec has only string fields — value copy is safe.
// Returns nil iff both inputs are nil-or-empty.
func mergeMarketplaceMap(base, override map[string]MarketplaceSpec) map[string]MarketplaceSpec {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := make(map[string]MarketplaceSpec, len(base)+len(override))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range override {
		out[k] = v
	}
	return out
}

// cloneMCPServerSpec returns a deep copy of s. MCPServerSpec has reference-type
// fields (Args []string, Env map[string]string) that must be copied to avoid aliasing.
func cloneMCPServerSpec(s MCPServerSpec) MCPServerSpec {
	out := s // copies Command, Grant, Cwd (strings)
	if s.Args != nil {
		out.Args = append([]string(nil), s.Args...)
	}
	if s.Env != nil {
		out.Env = make(map[string]string, len(s.Env))
		for k, v := range s.Env {
			out.Env[k] = v
		}
	}
	return out
}

// mergeMCPSpecMap merges two map[string]MCPServerSpec maps per-key;
// override wins per key. Each MCPServerSpec is deep-copied.
// Returns nil iff both inputs are nil-or-empty.
func mergeMCPSpecMap(base, override map[string]MCPServerSpec) map[string]MCPServerSpec {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := make(map[string]MCPServerSpec, len(base)+len(override))
	for k, v := range base {
		out[k] = cloneMCPServerSpec(v)
	}
	for k, v := range override {
		out[k] = cloneMCPServerSpec(v)
	}
	return out
}

// clonePolicyConfig returns a deep copy of p. keep.PolicyConfig has a Deny
// []string field that must be copied to avoid aliasing. Returns nil if p is nil.
func clonePolicyConfig(p *keep.PolicyConfig) *keep.PolicyConfig {
	if p == nil {
		return nil
	}
	c := *p // copies Pack, File, Mode (strings)
	if p.Deny != nil {
		c.Deny = append([]string(nil), p.Deny...)
	}
	return &c
}

// mergeLLMGatewayPtr merges two *LLMGatewayConfig pointers. Primary wins if
// non-nil; if both are non-nil, the pointed-to values are recursively merged.
// Returns nil if both are nil.
func mergeLLMGatewayPtr(primary, fallback *LLMGatewayConfig) *LLMGatewayConfig {
	if primary == nil && fallback == nil {
		return nil
	}
	if primary == nil {
		return &LLMGatewayConfig{Policy: clonePolicyConfig(fallback.Policy)}
	}
	if fallback == nil {
		return &LLMGatewayConfig{Policy: clonePolicyConfig(primary.Policy)}
	}
	// Both non-nil: project's Policy wins if set; fallback otherwise.
	return &LLMGatewayConfig{
		Policy: mergePolicyConfigPtr(primary.Policy, fallback.Policy),
	}
}

// mergePolicyConfigPtr merges two *keep.PolicyConfig pointers.
// Primary wins if non-nil; otherwise fallback is cloned. Returns nil if both nil.
func mergePolicyConfigPtr(primary, fallback *keep.PolicyConfig) *keep.PolicyConfig {
	if primary != nil {
		return clonePolicyConfig(primary)
	}
	return clonePolicyConfig(fallback)
}

// mergeUlimitMap merges two map[string]UlimitSpec maps per-key;
// override wins per key. UlimitSpec has only int64 fields — value copy is safe.
// Returns nil iff both inputs are nil-or-empty.
func mergeUlimitMap(base, override map[string]UlimitSpec) map[string]UlimitSpec {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := make(map[string]UlimitSpec, len(base)+len(override))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range override {
		out[k] = v
	}
	return out
}

// mergeNetworkRules merges two []netrules.NetworkRuleEntry slices keyed by
// Host. Override entries replace base entries on Host collision. Order is
// preserved: base entries first, then override-only entries appended.
// Returns nil iff both inputs are nil-or-empty.
//
// NetworkRuleEntry embeds HostRules{Host string, Rules []Rule}. Each entry is
// shallow-copied by value; Rules []Rule is a slice of Rule structs (all scalar
// string fields), so value-copy is safe (no pointer or map fields).
func mergeNetworkRules(base, override []netrules.NetworkRuleEntry) []netrules.NetworkRuleEntry {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	seen := make(map[string]int, len(base)+len(override))
	out := make([]netrules.NetworkRuleEntry, 0, len(base)+len(override))
	for _, r := range base {
		seen[r.Host] = len(out)
		out = append(out, cloneNetworkRuleEntry(r))
	}
	for _, r := range override {
		if idx, ok := seen[r.Host]; ok {
			out[idx] = cloneNetworkRuleEntry(r)
			continue
		}
		seen[r.Host] = len(out)
		out = append(out, cloneNetworkRuleEntry(r))
	}
	return out
}

// cloneNetworkRuleEntry returns a deep copy of e. HostRules.Rules is a
// []Rule slice (Rule has only string fields); it is copied to avoid aliasing.
func cloneNetworkRuleEntry(e netrules.NetworkRuleEntry) netrules.NetworkRuleEntry {
	out := e // copies Host (string)
	if e.Rules != nil {
		out.Rules = append([]netrules.Rule(nil), e.Rules...)
	}
	return out
}

// pickStr returns primary if non-empty, otherwise fallback.
func pickStr(primary, fallback string) string {
	if primary != "" {
		return primary
	}
	return fallback
}

// mergeStringMap merges two string-keyed string-valued maps.
// Returns nil iff both inputs are nil-or-empty (preserves omitempty YAML behavior).
func mergeStringMap(base, override map[string]string) map[string]string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := make(map[string]string, len(base)+len(override))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range override {
		out[k] = v
	}
	return out
}

// mergeIntMap merges two string-keyed int-valued maps.
func mergeIntMap(base, override map[string]int) map[string]int {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := make(map[string]int, len(base)+len(override))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range override {
		out[k] = v
	}
	return out
}

// mergeSlices handles slice fields on Config.
//
// String slices that represent "lists of independent capabilities or items"
// (Dependencies, Grants, LanguageServers) union with dedupe by string equality.
// String slices that represent "an ordered invocation" (Command) follow the
// scalar rule: project wins if non-empty, defaults fills otherwise.
// Struct slices (Mounts, Volumes, MCP) union with keyed dedupe; project wins
// on key collision.
func mergeSlices(d, p, out *Config) {
	out.Dependencies = unionDedupe(d.Dependencies, p.Dependencies)
	out.Grants = unionDedupe(d.Grants, p.Grants)
	out.LanguageServers = unionDedupe(d.LanguageServers, p.LanguageServers)
	out.Command = pickStrSlice(p.Command, d.Command)
	out.Mounts = mergeMounts(d.Mounts, p.Mounts)
	out.Volumes = mergeVolumes(d.Volumes, p.Volumes)
	out.MCP = mergeMCPServers(d.MCP, p.MCP)
}

// mergeMounts unions two []MountEntry slices, deduped by (Source, Target).
// Project entries replace defaults entries on key collision.
// Returns nil iff both inputs are nil-or-empty.
// MountEntry.Exclude is a []string, so each entry is deep-copied.
func mergeMounts(base, override []MountEntry) []MountEntry {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	type key struct{ Source, Target string }
	seen := make(map[key]int, len(base)+len(override))
	out := make([]MountEntry, 0, len(base)+len(override))
	for _, m := range base {
		k := key{m.Source, m.Target}
		seen[k] = len(out)
		out = append(out, cloneMountEntry(m))
	}
	for _, m := range override {
		k := key{m.Source, m.Target}
		if idx, ok := seen[k]; ok {
			out[idx] = cloneMountEntry(m)
			continue
		}
		seen[k] = len(out)
		out = append(out, cloneMountEntry(m))
	}
	return out
}

// cloneMountEntry returns a deep copy of m. MountEntry.Exclude is a []string
// reference type and must be copied to avoid aliasing.
func cloneMountEntry(m MountEntry) MountEntry {
	out := m // copies all scalar/bool/string fields
	if m.Exclude != nil {
		out.Exclude = append([]string(nil), m.Exclude...)
	}
	return out
}

// mergeVolumes unions two []VolumeConfig slices, deduped by Name.
// Project entries replace defaults entries on Name collision.
// Returns nil iff both inputs are nil-or-empty.
// VolumeConfig has no reference-type fields; value copy is safe.
func mergeVolumes(base, override []VolumeConfig) []VolumeConfig {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	seen := make(map[string]int, len(base)+len(override))
	out := make([]VolumeConfig, 0, len(base)+len(override))
	for _, v := range base {
		seen[v.Name] = len(out)
		out = append(out, v)
	}
	for _, v := range override {
		if idx, ok := seen[v.Name]; ok {
			out[idx] = v
			continue
		}
		seen[v.Name] = len(out)
		out = append(out, v)
	}
	return out
}

// mergeMCPServers unions two []MCPServerConfig slices, deduped by Name.
// Project entries replace defaults entries on Name collision.
// Returns nil iff both inputs are nil-or-empty.
// MCPServerConfig has pointer fields (Auth, Policy) that are deep-copied to
// avoid aliasing; keep.PolicyConfig.Deny is a []string that also needs copying.
func mergeMCPServers(base, override []MCPServerConfig) []MCPServerConfig {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	seen := make(map[string]int, len(base)+len(override))
	out := make([]MCPServerConfig, 0, len(base)+len(override))
	for _, m := range base {
		seen[m.Name] = len(out)
		out = append(out, cloneMCPServerConfig(m))
	}
	for _, m := range override {
		if idx, ok := seen[m.Name]; ok {
			out[idx] = cloneMCPServerConfig(m)
			continue
		}
		seen[m.Name] = len(out)
		out = append(out, cloneMCPServerConfig(m))
	}
	return out
}

// cloneMCPServerConfig returns a deep copy of m. The Auth and Policy pointer
// fields are copied so that mutating the clone does not affect the original.
// keep.PolicyConfig.Deny is a []string and is also deep-copied.
func cloneMCPServerConfig(m MCPServerConfig) MCPServerConfig {
	out := m // copies Name, URL (strings)
	if m.Auth != nil {
		authCopy := *m.Auth // MCPAuthConfig has only string fields
		out.Auth = &authCopy
	}
	if m.Policy != nil {
		policyCopy := *m.Policy // copies Pack, File, Mode (strings)
		if m.Policy.Deny != nil {
			policyCopy.Deny = append([]string(nil), m.Policy.Deny...)
		}
		out.Policy = &policyCopy
	}
	return out
}

// Source identifies where a resolved field's value came from.
type Source int

const (
	// SourceUnset means the field is zero in both inputs and merged result.
	SourceUnset Source = iota
	// SourceDefaults means the value came from ~/.moat/defaults.yaml.
	SourceDefaults
	// SourceProject means the value came from the project moat.yaml.
	SourceProject
	// SourceMerged means the value is the union/merge of both inputs
	// (used for maps and unioned slices where both sides contributed).
	SourceMerged
)

func (s Source) String() string {
	switch s {
	case SourceUnset:
		return "unset"
	case SourceDefaults:
		return "defaults"
	case SourceProject:
		return "project"
	case SourceMerged:
		return "merged"
	default:
		return "unknown"
	}
}

// SourceMap maps a yaml-style dotted path (e.g. "claude.base_url",
// "grants[aws]", "env.AWS_REGION") to the source of the resolved value at
// that path.
type SourceMap map[string]Source

// Sources computes the per-field origin of `merged` by diffing the
// resolved Config against `defaults` and `project`. For each leaf field
// reached, the source is determined by which input(s) carried the value.
//
// The function is computed post-hoc (no threading through MergeConfig) and
// is reflection-light: it walks Config's known fields explicitly, mirroring
// the structure of MergeConfig. Adding a new field to Config requires
// extending Sources to cover it.
func Sources(defaults, project, merged *Config) SourceMap {
	sm := SourceMap{}
	if defaults == nil {
		defaults = &Config{}
	}
	if project == nil {
		project = &Config{}
	}
	if merged == nil {
		return sm
	}

	// Scalars and pointers on top-level Config.
	annotateStr(sm, "name", defaults.Name, project.Name, merged.Name)
	annotateStr(sm, "agent", defaults.Agent, project.Agent, merged.Agent)
	annotateStr(sm, "version", defaults.Version, project.Version, merged.Version)
	annotateBool(sm, "interactive", defaults.Interactive, project.Interactive, merged.Interactive)
	annotateStr(sm, "sandbox", defaults.Sandbox, project.Sandbox, merged.Sandbox)
	annotateStr(sm, "runtime", defaults.Runtime, project.Runtime, merged.Runtime)
	annotateStr(sm, "base_image", defaults.BaseImage, project.BaseImage, merged.BaseImage)
	annotateBoolPtr(sm, "clipboard", defaults.Clipboard, project.Clipboard, merged.Clipboard)

	// Maps and slices: per-key/-element source.
	annotateStringMap(sm, "env", defaults.Env, project.Env)
	annotateStringMap(sm, "secrets", defaults.Secrets, project.Secrets)
	annotateIntMap(sm, "ports", defaults.Ports, project.Ports)
	annotateStringSlice(sm, "dependencies", defaults.Dependencies, project.Dependencies)
	annotateStringSlice(sm, "grants", defaults.Grants, project.Grants)
	annotateStringSlice(sm, "language_servers", defaults.LanguageServers, project.LanguageServers)

	// Nested struct: claude.
	annotateClaude(sm, "claude", defaults.Claude, project.Claude, merged.Claude)

	// Currently only top-level scalars, maps, string slices, and
	// claude.{base_url,sync_logs} are annotated. Other nested fields
	// (codex.*, gemini.*, container.*, network.*, snapshots.*, etc.)
	// pass through unannotated; extend annotateXxx helpers to broaden coverage.

	return sm
}

func annotateStr(sm SourceMap, path, d, p, m string) {
	switch {
	case m == "":
		sm[path] = SourceUnset
	case p != "" && p == m:
		sm[path] = SourceProject
	case d != "" && d == m:
		sm[path] = SourceDefaults
	default:
		sm[path] = SourceMerged
	}
}

func annotateBool(sm SourceMap, path string, d, p, m bool) {
	switch {
	case !m:
		sm[path] = SourceUnset
	case p && !d:
		sm[path] = SourceProject
	case d && !p:
		sm[path] = SourceDefaults
	default:
		sm[path] = SourceMerged
	}
}

func annotateBoolPtr(sm SourceMap, path string, d, p, m *bool) {
	if m == nil {
		sm[path] = SourceUnset
		return
	}
	if p != nil {
		sm[path] = SourceProject
		return
	}
	if d != nil {
		sm[path] = SourceDefaults
		return
	}
	sm[path] = SourceMerged
}

func annotateStringMap(sm SourceMap, path string, d, p map[string]string) {
	keys := map[string]struct{}{}
	for k := range d {
		keys[k] = struct{}{}
	}
	for k := range p {
		keys[k] = struct{}{}
	}
	for k := range keys {
		_, inP := p[k]
		_, inD := d[k]
		switch {
		case inP && !inD:
			sm[path+"."+k] = SourceProject
		case inD && !inP:
			sm[path+"."+k] = SourceDefaults
		case inD && inP:
			if p[k] == d[k] {
				sm[path+"."+k] = SourceMerged
			} else {
				sm[path+"."+k] = SourceProject // project overrode defaults
			}
		}
	}
}

func annotateIntMap(sm SourceMap, path string, d, p map[string]int) {
	keys := map[string]struct{}{}
	for k := range d {
		keys[k] = struct{}{}
	}
	for k := range p {
		keys[k] = struct{}{}
	}
	for k := range keys {
		_, inP := p[k]
		_, inD := d[k]
		switch {
		case inP && !inD:
			sm[path+"."+k] = SourceProject
		case inD && !inP:
			sm[path+"."+k] = SourceDefaults
		default:
			if p[k] == d[k] {
				sm[path+"."+k] = SourceMerged
			} else {
				sm[path+"."+k] = SourceProject
			}
		}
	}
}

func annotateStringSlice(sm SourceMap, path string, d, p []string) {
	inP := make(map[string]struct{}, len(p))
	for _, v := range p {
		inP[v] = struct{}{}
	}
	inD := make(map[string]struct{}, len(d))
	for _, v := range d {
		inD[v] = struct{}{}
	}
	all := make([]string, 0, len(p)+len(d))
	all = append(all, d...)
	for _, v := range p {
		if _, ok := inD[v]; !ok {
			all = append(all, v)
		}
	}
	for _, v := range all {
		_, fromP := inP[v]
		_, fromD := inD[v]
		switch {
		case fromP && !fromD:
			sm[path+"["+v+"]"] = SourceProject
		case fromD && !fromP:
			sm[path+"["+v+"]"] = SourceDefaults
		default:
			sm[path+"["+v+"]"] = SourceMerged
		}
	}
}

func annotateClaude(sm SourceMap, path string, d, p, m ClaudeConfig) {
	annotateStr(sm, path+".base_url", d.BaseURL, p.BaseURL, m.BaseURL)
	annotateBoolPtr(sm, path+".sync_logs", d.SyncLogs, p.SyncLogs, m.SyncLogs)
	// Add more per-field annotations here as needed for richer --source output.
}

// unionDedupe returns base ++ override with later duplicates removed
// (first occurrence wins; order: base first, then override additions).
// Duplicates within a single input are also collapsed (e.g. unionDedupe(["git","git"], nil) returns ["git"]).
// Returns nil iff both inputs are nil-or-empty.
func unionDedupe(base, override []string) []string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(base)+len(override))
	out := make([]string, 0, len(base)+len(override))
	for _, v := range base {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	for _, v := range override {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// pickStrSlice returns primary if non-empty, else fallback (no merge).
// Returns a fresh slice (no aliasing).
// Returns nil iff both inputs are nil-or-empty.
func pickStrSlice(primary, fallback []string) []string {
	if len(primary) > 0 {
		out := make([]string, len(primary))
		copy(out, primary)
		return out
	}
	if len(fallback) == 0 {
		return nil
	}
	out := make([]string, len(fallback))
	copy(out, fallback)
	return out
}

// cloneServiceSpec returns a deep copy of s so that mutating the clone's
// internal map fields does not affect the original.
func cloneServiceSpec(s ServiceSpec) ServiceSpec {
	out := s // value copy of scalar fields (Image, Memory)
	if s.Wait != nil {
		v := *s.Wait
		out.Wait = &v
	}
	if s.Env != nil {
		out.Env = make(map[string]string, len(s.Env))
		for k, v := range s.Env {
			out.Env[k] = v
		}
	}
	if s.Extra != nil {
		out.Extra = make(map[string][]string, len(s.Extra))
		for k, v := range s.Extra {
			out.Extra[k] = append([]string(nil), v...)
		}
	}
	return out
}

// mergeServicesMap merges Config.Services. ServiceSpec is treated as opaque —
// project's entry wins for a given key. Each ServiceSpec is deep-copied so
// mutating the returned map does not affect the input maps.
func mergeServicesMap(base, override map[string]ServiceSpec) map[string]ServiceSpec {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := make(map[string]ServiceSpec, len(base)+len(override))
	for k, v := range base {
		out[k] = cloneServiceSpec(v)
	}
	for k, v := range override {
		out[k] = cloneServiceSpec(v)
	}
	return out
}
