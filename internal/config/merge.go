package config

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
	// Nested-struct fields are filled by Tasks 4-5.
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

// cloneConfig returns a deep enough copy that mutating the returned Config
// does not affect the original. It is the identity merge with nil on the
// other side. It never returns nil.
func cloneConfig(c *Config) *Config {
	if c == nil {
		return &Config{}
	}
	// Implement clone by merging the input against an empty Config; the
	// merge functions copy each field defensively.
	empty := &Config{}
	out := &Config{}
	mergeScalars(empty, c, out)
	mergeMaps(empty, c, out)
	mergeSlices(empty, c, out)
	// Nested-struct fields filled in Tasks 4-5.
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
