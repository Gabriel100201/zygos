package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// useTempConfig points Path() at a throwaway file for the duration of a test.
func useTempConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "nested", "config.yaml")
	t.Setenv("ZYGOS_CONFIG", path)
	return path
}

func TestPathPrefersEnvOverride(t *testing.T) {
	path := useTempConfig(t)
	if got := Path(); got != path {
		t.Fatalf("Path() = %q, want %q", got, path)
	}
}

func TestPathFallsBackToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ZYGOS_CONFIG", "")
	t.Setenv("TABLERO_CONFIG", "")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	want := filepath.Join(home, ".zygos", "config.yaml")
	if got := Path(); got != want {
		t.Fatalf("Path() = %q, want %q", got, want)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	useTempConfig(t)

	want := &Config{Providers: []ProviderConfig{
		{Name: "work", Type: "linear", APIKey: "lin_api_example"},
		{Name: "selfhosted", Type: "taiga", URL: "https://taiga.example.com", Username: "your-username", Password: "your-password"},
		{Name: "op", Type: "openproject", URL: "https://op.example.com", APIKey: "op_api_example"},
	}}
	if err := want.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Providers) != len(want.Providers) {
		t.Fatalf("loaded %d providers, want %d", len(got.Providers), len(want.Providers))
	}
	for i, p := range got.Providers {
		if p != want.Providers[i] {
			t.Errorf("provider[%d] = %+v, want %+v", i, p, want.Providers[i])
		}
	}
}

// The config holds API keys and passwords in plaintext, so the permission bits
// are a security control, not a detail.
func TestSaveUsesOwnerOnlyPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits are not meaningful on Windows")
	}
	path := useTempConfig(t)

	cfg := &Config{Providers: []ProviderConfig{{Name: "work", Type: "linear", APIKey: "secret"}}}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("config file mode = %#o, want 0600", perm)
	}

	di, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Errorf("config dir mode = %#o, want 0700", perm)
	}
}

func TestLoadMissingFileSuggestsInit(t *testing.T) {
	useTempConfig(t)
	_, err := Load()
	if err == nil {
		t.Fatal("Load on a missing file should fail")
	}
	if !strings.Contains(err.Error(), "config add") {
		t.Errorf("error should point the user at the fix, got: %v", err)
	}
}

func TestLoadOrEmptyToleratesMissingFile(t *testing.T) {
	useTempConfig(t)
	cfg, err := LoadOrEmpty()
	if err != nil {
		t.Fatalf("LoadOrEmpty: %v", err)
	}
	if len(cfg.Providers) != 0 {
		t.Errorf("expected an empty config, got %d providers", len(cfg.Providers))
	}
}

func TestValidateRejectsIncompleteProviders(t *testing.T) {
	cases := []struct {
		name    string
		p       ProviderConfig
		wantErr string
	}{
		{"missing name", ProviderConfig{Type: "linear", APIKey: "k"}, "name is required"},
		{"unknown type", ProviderConfig{Name: "x", Type: "jira"}, "unknown type"},
		{"linear without key", ProviderConfig{Name: "x", Type: "linear"}, "api_key is required"},
		{"taiga without url", ProviderConfig{Name: "x", Type: "taiga", Username: "u", Password: "p"}, "url is required"},
		{"taiga without credentials", ProviderConfig{Name: "x", Type: "taiga", URL: "https://t.example"}, "username and password"},
		{"openproject without url", ProviderConfig{Name: "x", Type: "openproject", APIKey: "k"}, "url is required"},
		{"openproject without key", ProviderConfig{Name: "x", Type: "openproject", URL: "https://op.example"}, "api_key is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := (&Config{Providers: []ProviderConfig{tc.p}}).validate()
			if err == nil {
				t.Fatalf("expected an error for %+v", tc.p)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestValidateRejectsDuplicateNames(t *testing.T) {
	cfg := &Config{Providers: []ProviderConfig{
		{Name: "work", Type: "linear", APIKey: "a"},
		{Name: "work", Type: "linear", APIKey: "b"},
	}}
	err := cfg.validate()
	if err == nil || !strings.Contains(err.Error(), "duplicate name") {
		t.Fatalf("expected a duplicate-name error, got %v", err)
	}
}

// Names route tool calls to providers, so a collision must be caught on the way
// in rather than silently shadowing an existing workspace.
func TestAddProviderRejectsDuplicate(t *testing.T) {
	cfg := &Config{}
	first := ProviderConfig{Name: "work", Type: "linear", APIKey: "a"}
	if err := cfg.AddProvider(first); err != nil {
		t.Fatalf("AddProvider: %v", err)
	}
	if err := cfg.AddProvider(ProviderConfig{Name: "work", Type: "linear", APIKey: "b"}); err == nil {
		t.Fatal("adding a duplicate name should fail")
	}
	if len(cfg.Providers) != 1 {
		t.Fatalf("provider list mutated on failure: %d entries", len(cfg.Providers))
	}
	if cfg.Providers[0] != first {
		t.Errorf("existing provider was overwritten: %+v", cfg.Providers[0])
	}
}

func TestRemoveProvider(t *testing.T) {
	cfg := &Config{Providers: []ProviderConfig{
		{Name: "a", Type: "linear", APIKey: "1"},
		{Name: "b", Type: "linear", APIKey: "2"},
		{Name: "c", Type: "linear", APIKey: "3"},
	}}
	if err := cfg.RemoveProvider("b"); err != nil {
		t.Fatalf("RemoveProvider: %v", err)
	}
	if len(cfg.Providers) != 2 || cfg.Providers[0].Name != "a" || cfg.Providers[1].Name != "c" {
		t.Fatalf("unexpected remainder: %+v", cfg.Providers)
	}
	if err := cfg.RemoveProvider("nope"); err == nil {
		t.Error("removing an unknown provider should fail")
	}
}

// Save validates first: an invalid config must never overwrite a good file.
func TestSaveRejectsInvalidConfig(t *testing.T) {
	path := useTempConfig(t)
	good := &Config{Providers: []ProviderConfig{{Name: "work", Type: "linear", APIKey: "k"}}}
	if err := good.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	bad := &Config{Providers: []ProviderConfig{{Name: "work", Type: "linear"}}}
	if err := bad.Save(); err == nil {
		t.Fatal("saving an invalid config should fail")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(data), "api_key: k") {
		t.Errorf("the valid config on disk was clobbered:\n%s", data)
	}
}

func TestLoadRejectsMalformedYAML(t *testing.T) {
	path := useTempConfig(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("providers: [oops\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("malformed YAML should fail to load")
	}
}

// The project was called Tablero before v0.3.0. The rename must not strip an
// existing install of its providers.
func TestPathFallsBackToLegacyLocation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ZYGOS_CONFIG", "")
	t.Setenv("TABLERO_CONFIG", "")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows

	legacy := filepath.Join(home, ".tablero", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("providers: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := Path(); got != legacy {
		t.Errorf("Path() = %q, want the legacy config at %q", got, legacy)
	}
	if !UsingLegacyPath() {
		t.Error("UsingLegacyPath() = false, want true so the CLI can suggest moving it")
	}
}

// Once the current location exists it wins, even if the legacy one is still
// lying around.
func TestPathPrefersCurrentLocationOverLegacy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ZYGOS_CONFIG", "")
	t.Setenv("TABLERO_CONFIG", "")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	for _, dir := range []string{".tablero", ".zygos"} {
		path := filepath.Join(home, dir, "config.yaml")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("providers: []\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	want := filepath.Join(home, ".zygos", "config.yaml")
	if got := Path(); got != want {
		t.Errorf("Path() = %q, want %q", got, want)
	}
	if UsingLegacyPath() {
		t.Error("UsingLegacyPath() = true even though the current config exists")
	}
}

// A brand new install gets the current location, not the legacy one.
func TestPathDefaultsToCurrentLocation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ZYGOS_CONFIG", "")
	t.Setenv("TABLERO_CONFIG", "")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	want := filepath.Join(home, ".zygos", "config.yaml")
	if got := Path(); got != want {
		t.Errorf("Path() = %q, want %q", got, want)
	}
}

// The old environment variable keeps working for anyone who scripted it.
func TestLegacyEnvVarStillResolves(t *testing.T) {
	t.Setenv("ZYGOS_CONFIG", "")
	custom := filepath.Join(t.TempDir(), "wherever.yaml")
	t.Setenv("TABLERO_CONFIG", custom)

	if got := Path(); got != custom {
		t.Errorf("Path() = %q, want %q", got, custom)
	}
}

func TestZygosConfigWinsOverLegacyEnvVar(t *testing.T) {
	current := filepath.Join(t.TempDir(), "current.yaml")
	legacy := filepath.Join(t.TempDir(), "legacy.yaml")
	t.Setenv("ZYGOS_CONFIG", current)
	t.Setenv("TABLERO_CONFIG", legacy)

	if got := Path(); got != current {
		t.Errorf("Path() = %q, want %q", got, current)
	}
}

func TestDisplayPathAbbreviatesHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ZYGOS_CONFIG", "")
	t.Setenv("TABLERO_CONFIG", "")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	want := filepath.Join("~", ".zygos", "config.yaml")
	if got := DisplayPath(); got != want {
		t.Errorf("DisplayPath() = %q, want %q", got, want)
	}
}

// A config parked outside the home directory has no "~" to abbreviate to.
func TestDisplayPathLeavesOutsidePathsAlone(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	outside := filepath.Join(t.TempDir(), "elsewhere.yaml")
	t.Setenv("ZYGOS_CONFIG", outside)

	if got := DisplayPath(); got != outside {
		t.Errorf("DisplayPath() = %q, want the literal path %q", got, outside)
	}
}
