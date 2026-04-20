package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Configure the CLI, authenticate, and start the daemon",
	Long: `Configures the CLI to connect to Multica Cloud (multica.ai), then
authenticates via browser and starts the agent daemon.

If a configuration already exists, you will be prompted before overwriting.

Use 'multica setup self-host' to connect to a self-hosted server instead.

Use --profile to create an isolated configuration for a separate environment:
  multica setup self-host --profile staging --server-url https://api-staging.co`,
	RunE: runSetupCloud,
}

var setupCloudCmd = &cobra.Command{
	Use:   "cloud",
	Short: "Configure the CLI for Multica Cloud (multica.ai)",
	Long: `Explicitly configures the CLI to connect to Multica Cloud (multica.ai).

This is equivalent to running 'multica setup' without a subcommand.`,
	RunE: runSetupCloud,
}

var setupSelfHostCmd = &cobra.Command{
	Use:   "self-host",
	Short: "Configure the CLI for a self-hosted Multica server",
	Long: `Configures the CLI to connect to a self-hosted Multica server.

By default, connects to http://localhost:8080 (backend) and http://localhost:3000 (frontend).
Use --server-url and --app-url to specify a custom server (e.g. an on-premise deployment).

Examples:
  multica setup self-host
  multica setup self-host --server-url https://api.internal.co --app-url https://app.internal.co
  multica setup self-host --port 9090 --frontend-port 4000`,
	RunE: runSetupSelfHost,
}

func init() {
	setupSelfHostCmd.Flags().String("server-url", "", "Backend server URL (e.g. https://api.internal.co)")
	setupSelfHostCmd.Flags().String("app-url", "", "Frontend app URL (e.g. https://app.internal.co)")
	setupSelfHostCmd.Flags().Int("port", 8080, "Backend server port (used when --server-url is not set)")
	setupSelfHostCmd.Flags().Int("frontend-port", 3000, "Frontend port (used when --app-url is not set)")

	setupCmd.AddCommand(setupCloudCmd)
	setupCmd.AddCommand(setupSelfHostCmd)
}

// printConfigLocation prints the config file path and profile name.
func printConfigLocation(profile string) {
	path, err := cli.CLIConfigPathForProfile(profile)
	if err != nil {
		return
	}
	if profile != "" {
		fmt.Fprintf(os.Stderr, "  profile:    %s\n", profile)
	}
	fmt.Fprintf(os.Stderr, "  config:     %s\n", path)
}

// confirmOverwrite checks for an existing config and prompts the user.
// Returns true if we should proceed, false if the user declined.
func confirmOverwrite(profile string) (bool, error) {
	cfg, err := cli.LoadCLIConfigForProfile(profile)
	if err != nil {
		return true, nil // can't load -> treat as no config
	}
	if cfg.ServerURL == "" {
		return true, nil // no server configured -> fresh config
	}

	fmt.Fprintln(os.Stderr, "Current configuration:")
	fmt.Fprintf(os.Stderr, "  server_url: %s\n", cfg.ServerURL)
	fmt.Fprintf(os.Stderr, "  app_url:    %s\n", cfg.AppURL)
	if cfg.WorkspaceID != "" {
		fmt.Fprintf(os.Stderr, "  workspace:  %s\n", cfg.WorkspaceID)
	}
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprint(os.Stderr, "This will reset your configuration. Continue? [y/N] ")

	reader := bufio.NewReader(os.Stdin)
	answer, _ := reader.ReadString('\n')
	answer = strings.TrimSpace(strings.ToLower(answer))
	if answer != "y" && answer != "yes" {
		fmt.Fprintln(os.Stderr, "Aborted.")
		return false, nil
	}
	return true, nil
}

func runSetupCloud(cmd *cobra.Command, args []string) error {
	profile := resolveProfile(cmd)

	ok, err := confirmOverwrite(profile)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	cfg := cli.CLIConfig{
		ServerURL: "https://api.multica.ai",
		AppURL:    "https://multica.ai",
	}
	if err := cli.SaveCLIConfigForProfile(cfg, profile); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Fprintln(os.Stderr, "Configured for Multica Cloud (https://multica.ai).")
	fmt.Fprintf(os.Stderr, "  server_url: %s\n", cfg.ServerURL)
	fmt.Fprintf(os.Stderr, "  app_url:    %s\n", cfg.AppURL)
	printConfigLocation(profile)

	// Authenticate.
	fmt.Fprintln(os.Stderr, "")
	if err := runLogin(cmd, args); err != nil {
		return err
	}

	fmt.Fprintln(os.Stderr, "\nStarting daemon...")
	if err := runDaemonBackground(cmd); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}
	fmt.Fprintln(os.Stderr, "\n✓ Setup complete! Your machine is now connected to Multica.")

	return nil
}

func runSetupSelfHost(cmd *cobra.Command, args []string) error {
	profile := resolveProfile(cmd)

	ok, err := confirmOverwrite(profile)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	serverURL, _ := cmd.Flags().GetString("server-url")
	appURL, _ := cmd.Flags().GetString("app-url")
	port, _ := cmd.Flags().GetInt("port")
	frontendPort, _ := cmd.Flags().GetInt("frontend-port")

	serverURL, appURL = resolveSelfHostURLs(serverURL, appURL, port, frontendPort)

	cfg := cli.CLIConfig{
		ServerURL: serverURL,
		AppURL:    appURL,
	}
	if err := cli.SaveCLIConfigForProfile(cfg, profile); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Fprintln(os.Stderr, "Configured for self-hosted server.")
	fmt.Fprintf(os.Stderr, "  server_url: %s\n", cfg.ServerURL)
	fmt.Fprintf(os.Stderr, "  app_url:    %s\n", cfg.AppURL)
	printConfigLocation(profile)

	// Check if the server is reachable.
	if !probeServer(serverURL) {
		fmt.Fprintf(os.Stderr, "\n⚠ Server at %s is not reachable.\n", serverURL)
		fmt.Fprintln(os.Stderr, "  Make sure the server is running, then run 'multica login'.")
		return nil
	}

	// Authenticate.
	fmt.Fprintln(os.Stderr, "")
	if err := runLogin(cmd, args); err != nil {
		return err
	}

	fmt.Fprintln(os.Stderr, "\nStarting daemon...")
	if err := runDaemonBackground(cmd); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}
	fmt.Fprintln(os.Stderr, "\n✓ Setup complete! Your machine is now connected to Multica.")

	return nil
}

func resolveSelfHostURLs(serverURL, appURL string, port, frontendPort int) (string, string) {
	serverURL = strings.TrimSpace(serverURL)
	appURL = strings.TrimSpace(appURL)

	if serverURL == "" {
		serverURL = fmt.Sprintf("http://localhost:%d", port)
	}
	if appURL != "" {
		return serverURL, appURL
	}

	if inferred := inferAppURLFromServerURL(serverURL); inferred != "" {
		return serverURL, inferred
	}

	return serverURL, fmt.Sprintf("http://localhost:%d", frontendPort)
}

func inferAppURLFromServerURL(serverURL string) string {
	normalized := normalizeAPIBaseURL(serverURL)
	parsed, err := url.Parse(normalized)
	if err != nil || parsed.Host == "" {
		return ""
	}

	host := parsed.Hostname()
	if strings.EqualFold(host, "localhost") {
		return ""
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return ""
	}

	parsed.Path = ""
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/")
}

// probeServer checks whether a Multica backend is reachable at the given URL.
func probeServer(baseURL string) bool {
	client := &http.Client{Timeout: 2 * time.Second}

	for _, probe := range []struct {
		path        string
		statusCodes []int
	}{
		{path: "/health", statusCodes: []int{http.StatusOK}},
		{path: "/api/me", statusCodes: []int{http.StatusOK, http.StatusUnauthorized, http.StatusForbidden}},
	} {
		url := strings.TrimRight(baseURL, "/") + probe.path
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			cancel()
			continue
		}

		resp, err := client.Do(req)
		cancel()
		if err != nil {
			continue
		}
		resp.Body.Close()

		for _, code := range probe.statusCodes {
			if resp.StatusCode == code {
				return true
			}
		}
	}

	return false
}
