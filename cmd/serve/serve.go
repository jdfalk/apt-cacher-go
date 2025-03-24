package serve

import (
	"fmt"
	"log"
	"os"

	"github.com/jdfalk/apt-cacher-go/internal/config"
	"github.com/jdfalk/apt-cacher-go/internal/server"
	"github.com/spf13/cobra"
)

var (
	cfgFile    string
	cacheDir   string
	listenAddr string
	logLevel   string
)

// NewCommand returns the serve command
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the apt-cacher-go server",
		Long:  `Start the apt-cacher-go server to proxy and cache package requests`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe()
		},
	}

	// Add flags
	cmd.Flags().StringVar(&cfgFile, "config", "", "config file (default is /etc/apt-cacher-go/config.yaml)")
	cmd.Flags().StringVar(&cacheDir, "cache-dir", "/var/cache/apt-cacher-go", "Directory for cached packages")
	cmd.Flags().StringVar(&listenAddr, "listen", ":3142", "Address to listen on")
	cmd.Flags().StringVar(&logLevel, "log-level", "info", "Logging level (debug, info, warn, error)")

	return cmd
}

func runServe() error {
	// Configure from viper/flags
	cfg := &config.Config{
		CacheDir:      cacheDir,
		ListenAddress: listenAddr, // Fixed field name to match config.Config
		// LogLevel is not in Config, so we don't set it here
	}

	// Override with direct flag values if provided
	if cacheDir != "" {
		cfg.CacheDir = cacheDir
	}
	if listenAddr != "" {
		cfg.ListenAddress = listenAddr // Fixed field name
	}
	// Log level requires additional handling since it's not in config.Config

	// Ensure cache directory exists
	if err := os.MkdirAll(cfg.CacheDir, 0755); err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}

	// Create and start server
	srv, err := server.New(cfg) // Use server.New instead of server.NewServer
	if err != nil {
		return fmt.Errorf("failed to initialize server: %w", err)
	}

	log.Printf("Starting apt-cacher-go server on %s:%d\n", cfg.ListenAddress, cfg.Port)
	return srv.Start()
}
