package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Gabriel100201/zygos/internal/config"
	"github.com/Gabriel100201/zygos/internal/provider"
	"golang.org/x/term"
)

func cmdConfig(args []string) {
	if len(args) == 0 {
		printConfigUsage()
		os.Exit(1)
	}
	switch args[0] {
	case "init":
		cmdConfigInit()
	case "add":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: zygos config add <linear|taiga|openproject>")
			os.Exit(1)
		}
		cmdConfigAdd(args[1])
	case "list", "ls":
		cmdConfigList()
	case "remove", "rm":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: zygos config remove <name>")
			os.Exit(1)
		}
		cmdConfigRemove(args[1])
	case "test":
		name := ""
		if len(args) >= 2 {
			name = args[1]
		}
		cmdConfigTest(name)
	case "path":
		fmt.Println(config.Path())
		printLegacyPathNotice()
	case "help", "--help", "-h":
		printConfigUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown config subcommand: %s\n", args[0])
		printConfigUsage()
		os.Exit(1)
	}
}

func printConfigUsage() {
	fmt.Println(`zygos config — manage providers in ~/.zygos/config.yaml

USAGE
  zygos config init              Create an empty config file (if one doesn't exist)
  zygos config add linear        Add a Linear workspace (prompts for name + API key)
  zygos config add taiga         Add a Taiga instance (prompts for URL + credentials)
  zygos config add openproject   Add an OpenProject instance (prompts for URL + API key)
  zygos config list              List configured providers (secrets masked)
  zygos config remove <name>     Remove a provider by name
  zygos config test [name]       Verify connectivity to all (or a single) provider
  zygos config path              Print the resolved config file path
  zygos config help              Show this help

NOTES
  The config file is at $ZYGOS_CONFIG or ~/.zygos/config.yaml.
  It is saved with mode 0600 (owner read/write only) because it contains secrets.`)
}

// ─── init ─────────────────────────────────────────────────────────────────────

func cmdConfigInit() {
	path := config.Path()
	if _, err := os.Stat(path); err == nil {
		fmt.Printf("Config already exists at %s — nothing to do.\n", path)
		return
	}
	empty := &config.Config{Providers: []config.ProviderConfig{}}
	if err := empty.Save(); err != nil {
		fatalf("init: %v", err)
	}
	fmt.Printf("Created empty config at %s\n", path)
	fmt.Println("Next step: run `zygos config add linear` or `zygos config add taiga`.")
}

// ─── add ──────────────────────────────────────────────────────────────────────

func cmdConfigAdd(kind string) {
	kind = strings.ToLower(kind)
	if kind != "linear" && kind != "taiga" && kind != "openproject" {
		fatalf("add: unknown provider type %q (expected 'linear', 'taiga' or 'openproject')", kind)
	}

	cfg, err := config.LoadOrEmpty()
	if err != nil {
		fatalf("add: %v", err)
	}

	reader := bufio.NewReader(os.Stdin)
	pc := config.ProviderConfig{Type: kind}

	fmt.Printf("Adding a %s provider to %s\n\n", kind, config.Path())

	pc.Name = promptRequired(reader, "Unique provider name (e.g. 'work', 'personal')", func(s string) error {
		for _, existing := range cfg.Providers {
			if existing.Name == s {
				return fmt.Errorf("name %q already taken", s)
			}
		}
		return nil
	})

	switch kind {
	case "linear":
		pc.APIKey = promptSecret("Linear API key (Settings > API > Personal API Keys)")
	case "taiga":
		pc.URL = promptRequired(reader, "Taiga base URL (e.g. https://tree.taiga.io)", nil)
		pc.Username = promptRequired(reader, "Taiga username", nil)
		pc.Password = promptSecret("Taiga password")
	case "openproject":
		pc.URL = promptRequired(reader, "OpenProject base URL (e.g. https://openproject.example.com)", nil)
		pc.APIKey = promptSecret("OpenProject API key (My account > Access tokens > API)")
	}

	fmt.Println("\nValidating connection…")
	if err := validateProvider(pc); err != nil {
		fmt.Printf("Validation failed: %v\n", err)
		if !confirm(reader, "Save anyway?") {
			fmt.Println("Aborted. Nothing was written.")
			return
		}
	} else {
		fmt.Println("✓ Connection OK")
	}

	if err := cfg.AddProvider(pc); err != nil {
		fatalf("add: %v", err)
	}
	if err := cfg.Save(); err != nil {
		fatalf("add: saving config: %v", err)
	}
	fmt.Printf("\n✓ Provider %q saved.\n", pc.Name)
}

// ─── list ─────────────────────────────────────────────────────────────────────

func cmdConfigList() {
	cfg, err := config.LoadOrEmpty()
	if err != nil {
		fatalf("list: %v", err)
	}
	if len(cfg.Providers) == 0 {
		fmt.Println("No providers configured. Run `zygos config add linear` or `zygos config add taiga`.")
		return
	}
	fmt.Printf("Config file: %s\n", config.DisplayPath())
	printLegacyPathNotice()
	fmt.Println()
	fmt.Printf("%-24s %-8s %s\n", "NAME", "TYPE", "DETAILS")
	fmt.Println(strings.Repeat("-", 70))
	for _, p := range cfg.Providers {
		detail := ""
		switch p.Type {
		case "linear":
			detail = "API key: " + maskSecret(p.APIKey)
		case "taiga":
			detail = fmt.Sprintf("%s (user: %s, pass: %s)", p.URL, p.Username, maskSecret(p.Password))
		case "openproject":
			detail = fmt.Sprintf("%s (API key: %s)", p.URL, maskSecret(p.APIKey))
		}
		fmt.Printf("%-24s %-8s %s\n", p.Name, p.Type, detail)
	}
}

// ─── remove ───────────────────────────────────────────────────────────────────

func cmdConfigRemove(name string) {
	cfg, err := config.LoadOrEmpty()
	if err != nil {
		fatalf("remove: %v", err)
	}
	reader := bufio.NewReader(os.Stdin)
	if !confirm(reader, fmt.Sprintf("Remove provider %q?", name)) {
		fmt.Println("Aborted.")
		return
	}
	if err := cfg.RemoveProvider(name); err != nil {
		fatalf("remove: %v", err)
	}
	if err := cfg.Save(); err != nil {
		fatalf("remove: saving config: %v", err)
	}
	fmt.Printf("✓ Provider %q removed.\n", name)
}

// ─── test ─────────────────────────────────────────────────────────────────────

func cmdConfigTest(only string) {
	cfg, err := config.LoadOrEmpty()
	if err != nil {
		fatalf("test: %v", err)
	}
	if len(cfg.Providers) == 0 {
		fmt.Println("No providers configured.")
		return
	}
	fmt.Printf("Testing %d provider(s)…\n\n", len(cfg.Providers))
	anyFail := false
	for _, p := range cfg.Providers {
		if only != "" && p.Name != only {
			continue
		}
		fmt.Printf("  %-24s (%s) … ", p.Name, p.Type)
		if err := validateProvider(p); err != nil {
			fmt.Printf("✗ %v\n", err)
			anyFail = true
		} else {
			fmt.Println("✓ OK")
		}
	}
	if anyFail {
		os.Exit(1)
	}
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func validateProvider(pc config.ProviderConfig) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var p provider.Provider
	switch pc.Type {
	case "linear":
		p = provider.NewLinear(pc.Name, pc.APIKey)
	case "taiga":
		p = provider.NewTaiga(pc.Name, pc.URL, pc.Username, pc.Password)
	case "openproject":
		p = provider.NewOpenProject(pc.Name, pc.URL, pc.APIKey)
	default:
		return fmt.Errorf("unknown type %q", pc.Type)
	}

	return p.Ping(ctx)
}

// promptRequired keeps asking until non-empty input is given and passes validate().
func promptRequired(reader *bufio.Reader, label string, validate func(string) error) string {
	for {
		fmt.Printf("%s: ", label)
		line, err := reader.ReadString('\n')
		if err != nil {
			fatalf("reading input: %v", err)
		}
		line = strings.TrimSpace(line)
		if line == "" {
			fmt.Println("  (required — please enter a value)")
			continue
		}
		if validate != nil {
			if err := validate(line); err != nil {
				fmt.Printf("  invalid: %v\n", err)
				continue
			}
		}
		return line
	}
}

// promptSecret reads a value without echoing it to the terminal.
func promptSecret(label string) string {
	for {
		fmt.Printf("%s: ", label)
		fd := int(os.Stdin.Fd())
		var (
			raw []byte
			err error
		)
		if term.IsTerminal(fd) {
			raw, err = term.ReadPassword(fd)
			fmt.Println()
		} else {
			// stdin is a pipe (tests, automation) — fall back to line read.
			r := bufio.NewReader(os.Stdin)
			line, lerr := r.ReadString('\n')
			raw, err = []byte(strings.TrimRight(line, "\r\n")), lerr
		}
		if err != nil {
			fatalf("reading secret: %v", err)
		}
		s := strings.TrimSpace(string(raw))
		if s == "" {
			fmt.Println("  (required — please enter a value)")
			continue
		}
		return s
	}
}

func confirm(reader *bufio.Reader, question string) bool {
	fmt.Printf("%s [y/N]: ", question)
	line, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes"
}

func maskSecret(s string) string {
	if len(s) <= 6 {
		return "***"
	}
	return s[:3] + strings.Repeat("*", 6) + s[len(s)-3:]
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

// printLegacyPathNotice tells the user their config still lives under the
// project's previous name. It keeps working, but the path will not be looked up
// forever, so the move is worth making once.
func printLegacyPathNotice() {
	if !config.UsingLegacyPath() {
		return
	}
	fmt.Fprintln(os.Stderr, "\nNote: this config still lives in ~/.tablero, from before the project was renamed to Zygos.")
	fmt.Fprintln(os.Stderr, "It keeps working as is. To move it: mv ~/.tablero ~/.zygos")
}
