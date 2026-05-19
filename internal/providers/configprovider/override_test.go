package configprovider

import (
	"path/filepath"
	"testing"
)

func TestLoadEmbeddedDef_Gitlab(t *testing.T) {
	def, err := LoadEmbeddedDef("gitlab")
	if err != nil {
		t.Fatalf("LoadEmbeddedDef(\"gitlab\") error: %v", err)
	}
	if def.Name != "gitlab" {
		t.Errorf("Name = %q, want %q", def.Name, "gitlab")
	}
	if len(def.Hosts) == 0 || def.Hosts[0] != "gitlab.com" {
		t.Errorf("Hosts = %v, want first entry to be gitlab.com", def.Hosts)
	}
	if def.Validate == nil || def.Validate.URL != "https://gitlab.com/api/v4/user" {
		t.Errorf("Validate.URL = %+v, want https://gitlab.com/api/v4/user", def.Validate)
	}
}

func TestLoadEmbeddedDef_NotEmbedded(t *testing.T) {
	if _, err := LoadEmbeddedDef("github"); err == nil {
		t.Errorf("LoadEmbeddedDef(\"github\") err = nil, want error (github is a Go provider)")
	}
	if _, err := LoadEmbeddedDef("nonexistent"); err == nil {
		t.Errorf("LoadEmbeddedDef(\"nonexistent\") err = nil, want error")
	}
}

func TestUserOverridePath(t *testing.T) {
	t.Setenv("MOAT_HOME", "/tmp/moat-test")
	got := UserOverridePath("gitlab")
	want := filepath.Join("/tmp/moat-test", "providers", "gitlab.yaml")
	if got != want {
		t.Errorf("UserOverridePath(\"gitlab\") = %q, want %q", got, want)
	}
}

func TestEmbeddedProviderNames(t *testing.T) {
	names := EmbeddedProviderNames()
	if len(names) == 0 {
		t.Fatal("EmbeddedProviderNames() returned empty slice")
	}
	// Sorted, contains gitlab and at least one other.
	for i := 1; i < len(names); i++ {
		if names[i-1] >= names[i] {
			t.Errorf("EmbeddedProviderNames() not sorted: %v", names)
			break
		}
	}
	found := false
	for _, n := range names {
		if n == "gitlab" {
			found = true
		}
	}
	if !found {
		t.Errorf("EmbeddedProviderNames() = %v, missing gitlab", names)
	}
}
