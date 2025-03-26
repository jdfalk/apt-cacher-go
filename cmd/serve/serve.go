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

	// Add flags - keep current defaults for compatibility
	cmd.Flags().StringVar(&cfgFile, "config", "", "config file (default is /etc/apt-cacher-go/config.yaml)")
	cmd.Flags().StringVar(&cacheDir, "cache-dir", "", "Directory for cached packages (overrides config file)")
	cmd.Flags().StringVar(&listenAddr, "listen", "", "Address to listen on (overrides config file)")
	cmd.Flags().StringVar(&logLevel, "log-level", "info", "Logging level (debug, info, warn, error)")

	return cmd
}

func runServe() error {
	// Load configuration from file first
	var cfg *config.Config
	var err error

	if cfgFile != "" {
		// Load and validate with debug output to help troubleshoot
		log.Printf("Loading config from: %s", cfgFile)
		cfg, err = config.LoadConfigFileWithDebug(cfgFile)
	} else {
		// Try default locations
		defaultLocations := []string{
			"/etc/apt-cacher-go/config.yaml",
			"./config.yaml",
		}

		for _, loc := range defaultLocations {
			if _, err := os.Stat(loc); err == nil {
				log.Printf("Found config at default location: %s", loc)
				cfg, err = config.LoadConfigFileWithDebug(loc)
				if err == nil {
					break
				}
			}
		}

		// If still no config, use empty config
		if cfg == nil {
			cfg = &config.Config{
				CacheDir:      "/var/cache/apt-cacher-go",
				ListenAddress: "0.0.0.0",
				Port:          3142,
			}
			log.Printf("No config file found, using defaults")
		}
	}

	if err != nil {
		return fmt.Errorf("config loading error: %w", err)
	}

	// Override with command-line flags if provided
	if cacheDir != "" {
		log.Printf("Overriding cache_dir from flag: %s", cacheDir)
		cfg.CacheDir = cacheDir
	}

	if listenAddr != "" {
		log.Printf("Overriding listen_address from flag: %s", listenAddr)
		cfg.ListenAddress = listenAddr
	}

	// Log final configuration being used
	log.Printf("FINAL CONFIG - Using cache directory: %s", cfg.CacheDir)
	log.Printf("FINAL CONFIG - Using listen address: %s:%d", cfg.ListenAddress, cfg.Port)

	// Create and start server - removed our own MkdirAll since it's handled in server.New
	srv, err := server.New(cfg)
	if err != nil {
		return fmt.Errorf("server initialization failed: %w", err)
	}

	log.Printf("Starting apt-cacher-go server on %s:%d", cfg.ListenAddress, cfg.Port)
	return srv.Start()
}
