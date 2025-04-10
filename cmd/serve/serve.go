package serve

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jdfalk/apt-cacher-go/internal/config"
	"github.com/jdfalk/apt-cacher-go/internal/server"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	cfgFile    string
	cacheDir   string
	listenAddr string
	logLevel   string
)

// NewCommand returns the serve command for running the apt-cacher-go server.
//
// This function creates and configures a cobra.Command that handles the "serve"
// operation of apt-cacher-go. It sets up the command's flags, help text,
// and execution logic. The command loads configuration, initializes the server,
// sets up signal handling for graceful shutdown, and runs the server until
// interrupted.
//
// Returns:
//   - *cobra.Command: A fully configured command ready to be executed or added
//     to a parent command.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the apt-cacher-go server",
		Long:  `Start the apt-cacher-go server using the provided configuration.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Get the config file path from viper or use default
			configPath := viper.GetString("config")
			if configPath == "" {
				configPath = "/etc/apt-cacher-go/config.yaml"
			}

			log.Printf("Using config file: %s", configPath)
			cfg, err := config.LoadConfigFileWithDebug(configPath)
			if err != nil {
				return err
			}

			// Create server
			srv, err := server.New(cfg, server.ServerOptions{})
			if err != nil {
				return err
			}

			// Start the server
			if err := srv.Start(); err != nil {
				return err
			}

			// Block the main thread to keep the program running
			// Set up signal handling for graceful shutdown
			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

			// Block until we receive a termination signal
			sig := <-sigCh
			log.Printf("Received signal %v, shutting down gracefully...", sig)

			// Shutdown the server
			return srv.Shutdown()
		},
	}

	// Add flags - keep current defaults for compatibility
	cmd.Flags().StringVar(&cfgFile, "config", "", "config file (default is /etc/apt-cacher-go/config.yaml)")
	cmd.Flags().StringVar(&cacheDir, "cache-dir", "", "Directory for cached packages (overrides config file)")
	cmd.Flags().StringVar(&listenAddr, "listen", "", "Address to listen on (overrides config file)")
	cmd.Flags().StringVar(&logLevel, "log-level", "info", "Logging level (debug, info, warn, error)")

	return cmd
}

// runServe starts and manages the apt-cacher-go server with the provided configuration.
//
// This function initializes the server with the given configuration and version,
// starts it, and sets up signal handling for graceful shutdown. It blocks until
// a termination signal is received, then performs a clean shutdown with a timeout.
//
// Parameters:
//   - cfg: The configuration to use for the server
//   - version: The version string to use for server identification
//
// Returns:
//   - error: Any error encountered during server startup, operation, or shutdown
func runServe(cfg *config.Config, version string) error {
	// Create the server with ServerOptions
	srv, err := server.New(cfg, server.ServerOptions{
		Version: version,
	})
	if err != nil {
		return fmt.Errorf("failed to create server: %w", err)
	}

	// Start the server
	if err := srv.Start(); err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}

	// Block the main thread to keep the program running
	// Set up signal handling for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	// Block until we receive a termination signal
	sig := <-sigCh
	log.Printf("Received signal %v, shutting down gracefully...", sig)

	// Shutdown the server with timeout context
	_, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(); err != nil {
		return fmt.Errorf("failed to shutdown server: %w", err)
	}

	return nil
}
