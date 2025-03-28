package admin

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"
)

func newMigrateCommand() *cobra.Command {
	var acngCacheDir, acngConfFile, acgoCacheDir, acgoConfFile string

	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Migrate from apt-cacher-ng to apt-cacher-go",
		Long:  `Migrate configuration and cache from apt-cacher-ng to apt-cacher-go`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigration(acngCacheDir, acngConfFile, acgoCacheDir, acgoConfFile)
		},
	}

	// Add flags equivalent to shell script arguments
	cmd.Flags().StringVar(&acngCacheDir, "acng-cache", "/var/cache/apt-cacher-ng", "apt-cacher-ng cache directory")
	cmd.Flags().StringVar(&acngConfFile, "acng-conf", "/etc/apt-cacher-ng/acng.conf", "apt-cacher-ng config file")
	cmd.Flags().StringVar(&acgoCacheDir, "acgo-cache", "/var/cache/apt-cacher-go", "apt-cacher-go cache directory")
	cmd.Flags().StringVar(&acgoConfFile, "acgo-conf", "/etc/apt-cacher-go/config.yaml", "apt-cacher-go config file")

	return cmd
}

func runMigration(acngCacheDir, acngConfFile, acgoCacheDir, acgoConfFile string) error {
	// Check if apt-cacher-ng is installed
	_, err := exec.LookPath("apt-cacher-ng")
	if err != nil {
		return fmt.Errorf("apt-cacher-ng is not installed. Nothing to migrate")
	}

	fmt.Println("Starting migration from apt-cacher-ng to apt-cacher-go...")

	// Stop apt-cacher-ng service
	fmt.Println("Stopping apt-cacher-ng...")
	stopCmd := exec.Command("systemctl", "stop", "apt-cacher-ng")
	_ = stopCmd.Run() // Ignore errors as service might not be running

	// Create cache directory if it doesn't exist
	fmt.Printf("Creating cache directory: %s\n", acgoCacheDir)
	if err := os.MkdirAll(acgoCacheDir, 0755); err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}

	// Copy cache files
	fmt.Printf("Copying cache files from %s to %s...\n", acngCacheDir, acgoCacheDir)
	if err := copyDirectory(acngCacheDir, acgoCacheDir); err != nil {
		return fmt.Errorf("failed to copy cache files: %w", err)
	}

	// Extract configuration from apt-cacher-ng
	fmt.Println("Extracting configuration from apt-cacher-ng...")

	// Parse apt-cacher-ng configuration
	acngConfig, err := os.ReadFile(acngConfFile)
	if err != nil {
		return fmt.Errorf("failed to read apt-cacher-ng config: %w", err)
	}

	// Extract key values using regular expressions
	port := extractConfigValue(string(acngConfig), `Port:\s*(\d+)`, "3142")
	bindAddress := extractConfigValue(string(acngConfig), `BindAddress:\s*(.+)`, "0.0.0.0")
	maxSize := extractConfigValue(string(acngConfig), `CacheLimit:\s*(\d+)`, "40960")
	allowedIPs := extractConfigValue(string(acngConfig), `Allowed:\s*(.+)`, "")
	adminAuth := extractConfigValue(string(acngConfig), `AdminAuth:\s*(.+)`, "")

	// Create apt-cacher-go configuration
	fmt.Println("Creating apt-cacher-go configuration...")

	// Make sure the directory exists
	if err := os.MkdirAll(filepath.Dir(acgoConfFile), 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Build the config structure
	config := map[string]any{
		"listen_address":           bindAddress,
		"port":                     port,
		"cache_dir":                acgoCacheDir,
		"max_cache_size":           maxSize + "M", // Append M for megabytes
		"max_concurrent_downloads": 10,
	}

	// Add allowed IPs
	var allowedIPList []string
	if allowedIPs != "" {
		allowedIPList = strings.Fields(allowedIPs)
	} else {
		allowedIPList = []string{"0.0.0.0/0"}
	}
	config["allowed_ips"] = allowedIPList

	// Add admin auth
	if adminAuth != "" {
		config["admin_auth"] = true
		parts := strings.SplitN(adminAuth, ":", 2)
		if len(parts) == 2 {
			config["admin_user"] = parts[0]
			config["admin_password"] = parts[1]
		}
	} else {
		config["admin_auth"] = true
		config["admin_user"] = "admin"
		config["admin_password"] = "admin"
	}

	// Add repository backends
	config["backends"] = []map[string]any{
		{
			"name":     "ubuntu-archive",
			"url":      "http://archive.ubuntu.com/ubuntu",
			"priority": 100,
		},
		{
			"name":     "ubuntu-security",
			"url":      "http://security.ubuntu.com/ubuntu",
			"priority": 90,
		},
		{
			"name":     "debian",
			"url":      "http://deb.debian.org/debian",
			"priority": 80,
		},
		{
			"name":     "debian-security",
			"url":      "http://security.debian.org/debian-security",
			"priority": 70,
		},
	}

	// Write the configuration file
	yamlData, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Add a header comment
	configContent := "# Configuration migrated from apt-cacher-ng\n" + string(yamlData)

	if err := os.WriteFile(acgoConfFile, []byte(configContent), 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	fmt.Println("Migration completed successfully!")
	fmt.Printf("Configuration has been written to %s\n", acgoConfFile)
	fmt.Println("You can now install and start apt-cacher-go")

	return nil
}

func extractConfigValue(config, pattern, defaultValue string) string {
	re := regexp.MustCompile(pattern)
	matches := re.FindStringSubmatch(config)
	if len(matches) > 1 {
		return strings.TrimSpace(matches[1])
	}
	return defaultValue
}

func copyDirectory(src, dst string) error {
	// Create the destination directory if it doesn't exist
	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}

	// Use rsync for efficient directory copying
	cmd := exec.Command("rsync", "-a", src+"/", dst+"/")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
