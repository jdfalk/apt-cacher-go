// file: main.go
// Package main implements the command-line interface for apt-cacher-go, a caching proxy server for Debian/Ubuntu package repositories.
// It provides commands to start the server and run benchmarks.
// It also handles configuration loading from various sources, including command-line flags and configuration files.
// It uses the Cobra library for command-line parsing and Viper for configuration management.
// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/jdfalk/apt-cacher-go/cmd/admin"
	"github.com/jdfalk/apt-cacher-go/cmd/benchmark"
	"github.com/jdfalk/apt-cacher-go/cmd/serve"
	"github.com/jdfalk/apt-cacher-go/internal/config"
	"github.com/jdfalk/apt-cacher-go/internal/server"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	// Version information - to be set during build
	Version   = "dev"
	BuildTime = "unknown"
	GitCommit = "unknown"

	cfgFile string
	rootCmd = &cobra.Command{
		Use:   "apt-cacher-go",
		Short: "A caching proxy server for Debian/Ubuntu package repositories",
		Long: `apt-cacher-go is a caching proxy server for Debian/Ubuntu package repositories.
It helps reduce bandwidth usage by caching downloaded packages and serving them to multiple clients.`,
		Version: Version,
	}
)

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is /etc/apt-cacher-go/config.yaml)")

	// Silence usage on error to prevent confusion
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true

	// Add subcommands
	rootCmd.AddCommand(serve.NewCommand())
	rootCmd.AddCommand(benchmark.NewCommand())
	rootCmd.AddCommand(admin.NewCommand()) // Add the new admin command
}

func initConfig() {
	if cfgFile != "" {
		// Use config file from the flag
		viper.SetConfigFile(cfgFile)
	} else {
		// Search config in standard locations
		viper.AddConfigPath("/etc/apt-cacher-go/")
		viper.AddConfigPath("$HOME/.apt-cacher-go")
		viper.AddConfigPath(".")
		viper.SetConfigName("config")
		viper.SetConfigType("yaml")
	}

	// Read in environment variables that match
	viper.AutomaticEnv()
	viper.SetEnvPrefix("APT_CACHER")

	// If a config file is found, read it in
	if err := viper.ReadInConfig(); err == nil {
		fmt.Println("Using config file:", viper.ConfigFileUsed())
	}
}
func main() {
	// Load configuration
	configPath := "/etc/apt-cacher-go/config.yaml"
	if len(os.Args) > 1 {
		configPath = os.Args[1]
	}

	log.Printf("Using config file: %s", configPath)
	cfg, err := config.LoadConfigFileWithDebug(configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Create server
	srv, err := server.New(cfg, nil, nil, nil)
	if err != nil {
		log.Fatalf("Error: %v", err)
	}

	// Start the server
	if err := srv.Start(); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}

	// THIS IS THE FIX: Block the main thread to keep the program running
	// Set up signal handling for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	// Block until we receive a termination signal
	sig := <-sigCh
	log.Printf("Received signal %v, shutting down gracefully...", sig)

	// Shutdown the server
	if err := srv.Shutdown(); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}
}
