package admin

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
)

func newConfigureCommand() *cobra.Command {
	var host string
	var port, tlsPort int
	var useTLS bool

	cmd := &cobra.Command{
		Use:   "configure-apt",
		Short: "Configure a system to use apt-cacher-go",
		Long:  `Configure APT to use apt-cacher-go for package downloads`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return configureAptClient(host, port, tlsPort, useTLS)
		},
	}

	// Add flags equivalent to shell script arguments
	cmd.Flags().StringVarP(&host, "host", "h", "localhost", "Proxy hostname")
	cmd.Flags().IntVarP(&port, "port", "p", 3142, "Proxy HTTP port")
	cmd.Flags().IntVarP(&tlsPort, "tls-port", "s", 3143, "Proxy HTTPS port")
	cmd.Flags().BoolVarP(&useTLS, "use-tls", "t", false, "Configure to use HTTPS/TLS")

	return cmd
}

func configureAptClient(host string, port, tlsPort int, useTLS bool) error {
	// Check if running as root
	if os.Geteuid() != 0 {
		return fmt.Errorf("this command should be run as root to configure APT. Please re-run with sudo")
	}

	// Proxy configuration file path
	aptProxyConf := "/etc/apt/apt.conf.d/00proxy"

	// Create backup if file exists
	if _, err := os.Stat(aptProxyConf); err == nil {
		fmt.Println("Backing up existing proxy configuration...")
		if err := os.Rename(aptProxyConf, aptProxyConf+".bak"); err != nil {
			return fmt.Errorf("failed to backup existing config: %w", err)
		}
	}

	// Write new configuration
	fmt.Printf("Creating APT proxy configuration file at %s...\n", aptProxyConf)

	// Ensure the directory exists
	if err := os.MkdirAll(filepath.Dir(aptProxyConf), 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	var content string
	if useTLS {
		content = fmt.Sprintf(
			"// Proxy configuration for apt-cacher-go (HTTPS)\n"+
				"Acquire::http::Proxy \"http://%s:%d\";\n"+
				"Acquire::https::Proxy \"https://%s:%d\";\n",
			host, port, host, tlsPort)
	} else {
		content = fmt.Sprintf(
			"// Proxy configuration for apt-cacher-go (HTTP)\n"+
				"Acquire::http::Proxy \"http://%s:%d\";\n",
			host, port)
	}

	if err := os.WriteFile(aptProxyConf, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write configuration: %w", err)
	}

	fmt.Println("APT client configuration complete!")
	fmt.Printf("Your system will now use apt-cacher-go at %s for package downloads.\n", host)

	// Offer to test configuration
	fmt.Print("Would you like to test the configuration with 'apt update'? (y/n) ")
	var response string
	_, err := fmt.Scanln(&response)
	if err != nil {
		// Handle error gracefully - default to "no" if there's an input error
		fmt.Printf("Error reading input: %v. Assuming 'no'.\n", err)
		response = "n"
	}

	if response == "y" || response == "Y" || response == "yes" || response == "Yes" {
		fmt.Println("Running apt update to test configuration...")
		cmd := exec.Command("apt", "update")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("apt update failed: %w", err)
		}
		fmt.Println("Configuration test completed.")
	}

	return nil
}
