package serve

import (
	"log"
	"os"
	"os/signal"
	"syscall"

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

// NewCommand returns the serve command
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
			srv, err := server.New(cfg, nil, nil, nil)
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
