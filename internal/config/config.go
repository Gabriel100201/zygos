package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Names carried over from the project's previous identity. Kept so an existing
// install keeps working across the rename.
const (
	legacyDir    = ".tablero"
	legacyEnvVar = "TABLERO_CONFIG"
)

type Config struct {
	Providers []ProviderConfig `yaml:"providers"`
}

type ProviderConfig struct {
	Name     string `yaml:"name"`
	Type     string `yaml:"type"`     // "linear", "taiga" or "openproject"
	APIKey   string `yaml:"api_key"`  // Linear, OpenProject
	URL      string `yaml:"url"`      // Taiga, OpenProject base URL
	Username string `yaml:"username"` // Taiga
	Password string `yaml:"password"` // Taiga
}

// Load reads, parses, and validates the config file at Path().
func Load() (*Config, error) {
	path := Path()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no config found at %s — run `zygos config init` to create one, or `zygos config add linear` to add a provider interactively", path)
		}
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// LoadOrEmpty returns the existing config or an empty one if the file is missing.
// Used by CLI commands that need to mutate the config (add/remove) without failing
// when no config exists yet.
func LoadOrEmpty() (*Config, error) {
	path := Path()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, fmt.Errorf("reading config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	return &cfg, nil
}

// Save writes the config to Path(), creating parent directories if needed.
// On Unix-like systems the file is written with mode 0600 (owner-only) since it
// contains API keys and passwords.
func (c *Config) Save() error {
	if err := c.validate(); err != nil {
		return err
	}
	path := Path()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	return nil
}

// Path returns the resolved config file path.
//
// Resolution order: $ZYGOS_CONFIG, then the legacy $TABLERO_CONFIG, then
// ~/.zygos/config.yaml, then ~/.tablero/config.yaml if that is the only one
// that exists. The project was called Tablero before v0.3.0 and the rename must
// not silently strip an existing installation of its providers; a config
// written by the old binary keeps working where it is until the user moves it.
func Path() string {
	if p := os.Getenv("ZYGOS_CONFIG"); p != "" {
		return p
	}
	if p := os.Getenv(legacyEnvVar); p != "" {
		return p
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".zygos", "config.yaml")
	}

	current := filepath.Join(home, ".zygos", "config.yaml")
	if _, err := os.Stat(current); err == nil {
		return current
	}
	if legacy := filepath.Join(home, legacyDir, "config.yaml"); fileExists(legacy) {
		return legacy
	}
	return current
}

// UsingLegacyPath reports whether the config in use is still the pre-rename
// one, so the CLI can suggest moving it.
func UsingLegacyPath() bool {
	return filepath.Base(filepath.Dir(Path())) == legacyDir
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// DisplayPath is Path() with the home directory abbreviated to "~", for output
// meant to be read rather than consumed. Commands whose output gets piped —
// `config path` — must keep printing the literal path.
func DisplayPath() string {
	path := Path()
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if rel, err := filepath.Rel(home, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.Join("~", rel)
	}
	return path
}

// AddProvider appends a provider or returns an error if the name is already taken.
func (c *Config) AddProvider(p ProviderConfig) error {
	for _, existing := range c.Providers {
		if existing.Name == p.Name {
			return fmt.Errorf("provider %q already exists", p.Name)
		}
	}
	c.Providers = append(c.Providers, p)
	return nil
}

// RemoveProvider deletes a provider by name. Returns an error if it doesn't exist.
func (c *Config) RemoveProvider(name string) error {
	for i, p := range c.Providers {
		if p.Name == name {
			c.Providers = append(c.Providers[:i], c.Providers[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("provider %q not found", name)
}

func (c *Config) validate() error {
	names := make(map[string]bool)
	for i, p := range c.Providers {
		if p.Name == "" {
			return fmt.Errorf("provider[%d]: name is required", i)
		}
		if names[p.Name] {
			return fmt.Errorf("provider[%d]: duplicate name %q", i, p.Name)
		}
		names[p.Name] = true

		switch p.Type {
		case "linear":
			if p.APIKey == "" {
				return fmt.Errorf("provider %q (linear): api_key is required", p.Name)
			}
		case "taiga":
			if p.URL == "" {
				return fmt.Errorf("provider %q (taiga): url is required", p.Name)
			}
			if p.Username == "" || p.Password == "" {
				return fmt.Errorf("provider %q (taiga): username and password are required", p.Name)
			}
		case "openproject":
			if p.URL == "" {
				return fmt.Errorf("provider %q (openproject): url is required", p.Name)
			}
			if p.APIKey == "" {
				return fmt.Errorf("provider %q (openproject): api_key is required", p.Name)
			}
		default:
			return fmt.Errorf("provider %q: unknown type %q (must be 'linear', 'taiga' or 'openproject')", p.Name, p.Type)
		}
	}
	return nil
}
