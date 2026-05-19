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

func TestApplyHostOverride_Gitlab(t *testing.T) {
	def, err := LoadEmbeddedDef("gitlab")
	if err != nil {
		t.Fatalf("LoadEmbeddedDef: %v", err)
	}
	out, err := ApplyHostOverride(def, "gitlab.acme.com")
	if err != nil {
		t.Fatalf("ApplyHostOverride: %v", err)
	}
	if len(out.Hosts) != 1 || out.Hosts[0] != "gitlab.acme.com" {
		t.Errorf("Hosts = %v, want [gitlab.acme.com]", out.Hosts)
	}
	if out.Validate == nil || out.Validate.URL != "https://gitlab.acme.com/api/v4/user" {
		t.Errorf("Validate.URL = %+v, want https://gitlab.acme.com/api/v4/user", out.Validate)
	}
	// Other fields preserved.
	if out.Name != def.Name || out.Description != def.Description ||
		out.Inject.Header != def.Inject.Header || out.ContainerEnv != def.ContainerEnv {
		t.Errorf("non-host fields changed: got %+v, original %+v", out, def)
	}
	// Original def must not be mutated.
	if len(def.Hosts) == 1 && def.Hosts[0] == "gitlab.acme.com" {
		t.Errorf("ApplyHostOverride mutated input def")
	}
}

func TestApplyHostOverride_NoValidate(t *testing.T) {
	def := ProviderDef{
		Name:        "x",
		Description: "x",
		Hosts:       []string{"x.example.com"},
		Inject:      InjectConfig{Header: "X"},
	}
	out, err := ApplyHostOverride(def, "y.example.com")
	if err != nil {
		t.Fatalf("ApplyHostOverride: %v", err)
	}
	if out.Hosts[0] != "y.example.com" {
		t.Errorf("Hosts[0] = %q, want y.example.com", out.Hosts[0])
	}
	if out.Validate != nil {
		t.Errorf("Validate = %+v, want nil", out.Validate)
	}
}

func TestApplyHostOverride_PreservesTokenPlaceholder(t *testing.T) {
	def := ProviderDef{
		Name:         "telegram",
		Description:  "x",
		Hosts:        []string{"api.telegram.org"},
		Inject:       InjectConfig{Header: ""},
		ContainerEnv: "TELEGRAM_BOT_TOKEN",
		Validate: &ValidateConfig{
			URL: "https://api.telegram.org/bot${token}/getMe",
		},
	}
	out, err := ApplyHostOverride(def, "tg.example.com")
	if err != nil {
		t.Fatalf("ApplyHostOverride: %v", err)
	}
	want := "https://tg.example.com/bot${token}/getMe"
	if out.Validate.URL != want {
		t.Errorf("Validate.URL = %q, want %q", out.Validate.URL, want)
	}
}

func TestApplyHostOverride_RelativeValidateURL(t *testing.T) {
	def := ProviderDef{
		Name:        "x",
		Description: "x",
		Hosts:       []string{"x.example.com"},
		Inject:      InjectConfig{Header: "X"},
		Validate:    &ValidateConfig{URL: "/relative/path"},
	}
	if _, err := ApplyHostOverride(def, "y.example.com"); err == nil {
		t.Errorf("ApplyHostOverride err = nil, want error for relative validate URL")
	}
}

func TestApplyHostOverride_UserinfoValidateURL(t *testing.T) {
	def := ProviderDef{
		Name:        "x",
		Description: "x",
		Hosts:       []string{"x.example.com"},
		Inject:      InjectConfig{Header: "X"},
		Validate:    &ValidateConfig{URL: "https://user:pass@x.example.com/api"},
	}
	if _, err := ApplyHostOverride(def, "y.example.com"); err == nil {
		t.Errorf("ApplyHostOverride err = nil, want error for validate URL with userinfo")
	}
}
