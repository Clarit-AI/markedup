package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/Clarit-AI/markedup/config"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// cloudPreset describes a cloud provider option shown in the setup menu.
type cloudPreset struct {
	Name     string
	Endpoint string
	NeedsKey bool
}

var cloudPresets = []cloudPreset{
	{Name: "OpenRouter", Endpoint: "https://openrouter.ai/api", NeedsKey: true},
	{Name: "OpenAI", Endpoint: "https://api.openai.com", NeedsKey: true},
	{Name: "Ollama Cloud", Endpoint: "https://api.ollama.com", NeedsKey: true},
	{Name: "Custom", Endpoint: "", NeedsKey: false},
}

// setupDeps holds injectable dependencies for testing.
type setupDeps struct {
	Stdin          io.Reader
	Stdout         io.Writer
	DetectLocal    func(ctx context.Context) []config.Endpoint
	Save           func(cfg *config.Config, path string) error
	StoreKey       func(name, value string) error
	KeyringAvail   func() bool
	GlobalPath     func() string
	ReadPassword   func(fd int) ([]byte, error)
	StdinFd        func() int
	IsTerminal     func(fd int) bool
}

func defaultDeps() *setupDeps {
	return &setupDeps{
		Stdin:        os.Stdin,
		Stdout:       os.Stdout,
		DetectLocal:  config.DetectLocal,
		Save:         config.Save,
		StoreKey:     config.StoreKey,
		KeyringAvail: config.KeyringAvailable,
		GlobalPath:   config.GlobalPath,
		ReadPassword: term.ReadPassword,
		StdinFd:      func() int { return int(os.Stdin.Fd()) },
		IsTerminal:   func(fd int) bool { return term.IsTerminal(fd) },
	}
}

func newSetupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Interactive configuration wizard",
		Long:  "Walk through text-based prompts to configure embedding, LLM, and reranker endpoints.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSetup(cmd.Context(), defaultDeps())
		},
	}
}

func runSetup(ctx context.Context, d *setupDeps) error {
	// setup writes credentials and benefits from migrating legacy entries
	// before recording new ones (issue #153: lazy, opt-in hydration).
	_ = config.HydrateKeys(appConfig)

	w := d.Stdout
	scanner := bufio.NewScanner(d.Stdin)

	fmt.Fprintln(w, "=== markedup setup ===")
	fmt.Fprintln(w)

	// Step 1: Auto-detect
	fmt.Fprintln(w, "Scanning for local model servers...")
	endpoints := d.DetectLocal(ctx)
	if len(endpoints) == 0 {
		fmt.Fprintln(w, "  No local servers detected.")
	} else {
		for _, ep := range endpoints {
			models := ""
			if len(ep.Models) > 0 {
				models = " [" + strings.Join(ep.Models, ", ") + "]"
			}
			fmt.Fprintf(w, "  ✓ %s (%s) — %s%s\n", ep.Name, ep.URL, ep.Type, models)
		}
	}
	fmt.Fprintln(w)

	cfg := &config.Config{}

	// Step 2: Configure each service
	embedSvc, embedKey, err := promptService(w, scanner, d, "Embedding", "embed", endpoints)
	if err != nil {
		return err
	}
	cfg.Embed = embedSvc

	llmSvc, llmKey, err := promptService(w, scanner, d, "LLM", "llm", endpoints)
	if err != nil {
		return err
	}
	cfg.LLM = llmSvc

	rerankSvc, rerankKey, err := promptService(w, scanner, d, "Reranker (optional)", "rerank", endpoints)
	if err != nil {
		return err
	}
	cfg.Rerank = config.RerankConfig{ServiceConfig: rerankSvc}

	// Step 3: Summary
	fmt.Fprintln(w)
	fmt.Fprintln(w, "=== Configuration Summary ===")
	printServiceSummary(w, "Embed", cfg.Embed)
	printServiceSummary(w, "LLM", cfg.LLM)
	printServiceSummary(w, "Rerank", cfg.Rerank.ServiceConfig)
	fmt.Fprintln(w)

	fmt.Fprint(w, "Save configuration? (y/n): ")
	if !scanner.Scan() {
		return scanner.Err()
	}
	answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
	if answer != "y" && answer != "yes" {
		fmt.Fprintln(w, "Setup cancelled.")
		return nil
	}

	// Step 4: Save config
	path := d.GlobalPath()
	if err := d.Save(cfg, path); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}
	fmt.Fprintf(w, "Configuration saved to %s\n", path)

	// Step 5: Store API keys, deduplicated by endpoint (issue #117). Two
	// services pointing at the same provider share a single keyring entry,
	// so the OS keychain only prompts to unlock once per launch.
	type pendingKey struct {
		serviceLabel string
		endpoint     string
		value        string
	}
	pending := []pendingKey{
		{"embed", cfg.Embed.Endpoint, embedKey},
		{"llm", cfg.LLM.Endpoint, llmKey},
		{"rerank", cfg.Rerank.Endpoint, rerankKey},
	}

	if d.KeyringAvail() {
		written := map[string]bool{}
		for _, pk := range pending {
			if pk.value == "" {
				continue
			}
			name := config.KeyNameForEndpoint(pk.endpoint)
			if name == "" {
				fmt.Fprintf(w, "Warning: no endpoint configured for %s — key not stored in keyring\n", pk.serviceLabel)
				continue
			}
			if written[name] {
				continue
			}
			if err := d.StoreKey(name, pk.value); err != nil {
				fmt.Fprintf(w, "Warning: failed to store %s key in keyring: %v\n", pk.serviceLabel, err)
				continue
			}
			written[name] = true
		}
		fmt.Fprintln(w, "API keys stored in OS keyring.")
	} else {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Keyring not available. Set API keys as environment variables in your shell profile:")
		if embedKey != "" {
			fmt.Fprintln(w, "  export MARKEDUP_EMBED_API_KEY=<your-key>")
		}
		if llmKey != "" {
			fmt.Fprintln(w, "  export MARKEDUP_LLM_API_KEY=<your-key>")
		}
		if rerankKey != "" {
			fmt.Fprintln(w, "  export MARKEDUP_RERANK_API_KEY=<your-key>")
		}
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "Setup complete!")
	return nil
}

// promptService presents a numbered menu for choosing a provider for the given
// service type and returns the ServiceConfig and any API key entered.
func promptService(w io.Writer, scanner *bufio.Scanner, d *setupDeps, label, serviceType string, detected []config.Endpoint) (config.ServiceConfig, string, error) {
	fmt.Fprintf(w, "--- %s provider ---\n", label)

	// Build options list
	type option struct {
		label    string
		endpoint string
		models   []string
		needsKey bool
		isCustom bool
		isSkip   bool
	}

	var opts []option

	// Add detected local endpoints matching this service type
	for _, ep := range detected {
		if ep.Type == serviceType || ep.Type == "multi" {
			opts = append(opts, option{
				label:    fmt.Sprintf("%s (local — %s)", ep.Name, ep.URL),
				endpoint: ep.URL,
				models:   ep.Models,
			})
		}
	}

	// Add cloud presets
	for _, cp := range cloudPresets {
		if cp.Name == "Custom" {
			opts = append(opts, option{
				label:    "Custom endpoint",
				isCustom: true,
			})
		} else {
			opts = append(opts, option{
				label:    cp.Name,
				endpoint: cp.Endpoint,
				needsKey: cp.NeedsKey,
			})
		}
	}

	// Add skip
	opts = append(opts, option{label: "Skip", isSkip: true})

	// Display
	for i, o := range opts {
		fmt.Fprintf(w, "  %d) %s\n", i+1, o.label)
	}

	choice, err := promptInt(w, scanner, "Choose", 1, len(opts))
	if err != nil {
		return config.ServiceConfig{}, "", err
	}

	selected := opts[choice-1]
	if selected.isSkip {
		fmt.Fprintln(w)
		return config.ServiceConfig{}, "", nil
	}

	svc := config.ServiceConfig{
		Endpoint: selected.endpoint,
	}

	// Custom endpoint
	if selected.isCustom {
		fmt.Fprint(w, "  Endpoint URL: ")
		if !scanner.Scan() {
			return config.ServiceConfig{}, "", scanner.Err()
		}
		svc.Endpoint = strings.TrimSpace(scanner.Text())
	}

	// Model name — show detected models or prompt freely
	if len(selected.models) > 0 {
		fmt.Fprintln(w, "  Available models:")
		for i, m := range selected.models {
			fmt.Fprintf(w, "    %d) %s\n", i+1, m)
		}
		fmt.Fprint(w, "  Model (number or name): ")
		if !scanner.Scan() {
			return config.ServiceConfig{}, "", scanner.Err()
		}
		input := strings.TrimSpace(scanner.Text())
		if n, err := strconv.Atoi(input); err == nil && n >= 1 && n <= len(selected.models) {
			svc.Model = selected.models[n-1]
		} else {
			svc.Model = input
		}
	} else {
		fmt.Fprint(w, "  Model name: ")
		if !scanner.Scan() {
			return config.ServiceConfig{}, "", scanner.Err()
		}
		svc.Model = strings.TrimSpace(scanner.Text())
	}

	// API key
	var apiKey string
	if selected.needsKey || selected.isCustom {
		fmt.Fprint(w, "  API key (leave empty if none): ")
		apiKey, err = readMasked(w, scanner, d)
		if err != nil {
			return config.ServiceConfig{}, "", err
		}
	}

	fmt.Fprintln(w)
	return svc, apiKey, nil
}

// readMasked attempts to read a password with terminal echo disabled.
// Falls back to plain-text input when stdin is not a terminal (e.g. piped).
func readMasked(w io.Writer, scanner *bufio.Scanner, d *setupDeps) (string, error) {
	fd := d.StdinFd()
	if d.IsTerminal(fd) {
		pw, err := d.ReadPassword(fd)
		fmt.Fprintln(w) // newline after hidden input
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(pw)), nil
	}

	// Non-terminal fallback (agent / piped input)
	if !scanner.Scan() {
		return "", scanner.Err()
	}
	return strings.TrimSpace(scanner.Text()), nil
}

// promptInt asks the user to enter a number between min and max (inclusive).
func promptInt(w io.Writer, scanner *bufio.Scanner, prompt string, min, max int) (int, error) {
	for {
		fmt.Fprintf(w, "  %s [%d-%d]: ", prompt, min, max)
		if !scanner.Scan() {
			return 0, scanner.Err()
		}
		text := strings.TrimSpace(scanner.Text())
		n, err := strconv.Atoi(text)
		if err == nil && n >= min && n <= max {
			return n, nil
		}
		fmt.Fprintf(w, "  Please enter a number between %d and %d.\n", min, max)
	}
}

func printServiceSummary(w io.Writer, label string, svc config.ServiceConfig) {
	if svc.Endpoint == "" && svc.Model == "" {
		fmt.Fprintf(w, "  %s: (skipped)\n", label)
		return
	}
	fmt.Fprintf(w, "  %s: %s — %s\n", label, svc.Endpoint, svc.Model)
}
