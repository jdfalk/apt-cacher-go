package serve

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/jdfalk/apt-cacher-go/internal/config"
	"github.com/jdfalk/apt-cacher-go/internal/server"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// Command line flags
var (
	listenAddress string
	port          int
	cacheDir      string
	tlsEnabled    bool
	tlsPort       int
	tlsCertFile   string
	tlsKeyFile    string
	maxCacheSize  int64
)

// NewCommand creates the serve command
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the apt-cacher-go server",
		Long:  `Start the apt-cacher-go server to provide caching proxy services for Debian/Ubuntu repositories.`,
		Run: func(cmd *cobra.Command, args []string) {
			runServer()
		},
	}

	// Define flags
	cmd.Flags().StringVarP(&listenAddress, "listen", "l", "0.0.0.0", "Address to listen on")
	cmd.Flags().IntVarP(&port, "port", "p", 3142, "Port to listen on")
	cmd.Flags().StringVarP(&cacheDir, "cache-dir", "c", "/var/cache/apt-cacher-go", "Directory for cached packages")
	cmd.Flags().BoolVar(&tlsEnabled, "tls", false, "Enable TLS/HTTPS")
	cmd.Flags().IntVar(&tlsPort, "tls-port", 3143, "Port for TLS/HTTPS")
	cmd.Flags().StringVar(&tlsCertFile, "tls-cert", "", "TLS certificate file")
	cmd.Flags().StringVar(&tlsKeyFile, "tls-key", "", "TLS key file")
	cmd.Flags().Int64Var(&maxCacheSize, "max-cache-size", 1024*1024*1024, "Maximum cache size in bytes (default 1GB)")

	// Bind flags to viper
	viper.BindPFlag("listen_address", cmd.Flags().Lookup("listen"))
	viper.BindPFlag("port", cmd.Flags().Lookup("port"))
	viper.BindPFlag("cache_dir", cmd.Flags().Lookup("cache-dir"))
	viper.BindPFlag("tls.enabled", cmd.Flags().Lookup("tls"))
	viper.BindPFlag("tls.port", cmd.Flags().Lookup("tls-port"))
	viper.BindPFlag("tls.cert_file", cmd.Flags().Lookup("tls-cert"))
	viper.BindPFlag("tls.key_file", cmd.Flags().Lookup("tls-key"))
	viper.BindPFlag("max_cache_size", cmd.Flags().Lookup("max-cache-size"))

	return cmd
}

func runServer() {
	// Create configuration from viper values
	cfg := &config.Config{
		ListenAddress:          viper.GetString("listen_address"),
		Port:                   viper.GetInt("port"),
		CacheDir:               viper.GetString("cache_dir"),
		MaxCacheSize:           viper.GetInt64("max_cache_size"),
		TLSEnabled:             viper.GetBool("tls.enabled"),
		TLSPort:                viper.GetInt("tls.port"),
		TLSCertFile:            viper.GetString("tls.cert_file"),
		TLSKeyFile:             viper.GetString("tls.key_file"),
		MaxConcurrentDownloads: viper.GetInt("max_concurrent_downloads"),
	}

	// Set default for max concurrent downloads if not provided
	if cfg.MaxConcurrentDownloads == 0 {
		cfg.MaxConcurrentDownloads = 10
	}

	// Ensure cache directory exists
	if err := os.MkdirAll(cfg.CacheDir, 0755); err != nil {
		log.Fatalf("Failed to create cache directory: %v", err)
	}

	// Initialize server
	srv, err := server.New(cfg)
	if err != nil {
		log.Fatalf("Failed to initialize server: %v", err)
	}

	// Start server in a goroutine
	go func() {
		if err := srv.Start(); err != nil {
			log.Fatalf("Server error: %v", err)
		}
	}()

	fmt.Printf("apt-cacher-go server started on %s:%d\n", cfg.ListenAddress, cfg.Port)
	if cfg.TLSEnabled {
		fmt.Printf("TLS enabled on port %d\n", cfg.TLSPort)
	}

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	fmt.Println("Shutting down server...")
	if err := srv.Shutdown(); err != nil {
		log.Fatalf("Server shutdown error: %v", err)
	}
}
