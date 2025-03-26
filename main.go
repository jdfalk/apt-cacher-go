// file: main.go
// Package main implements the command-line interface for apt-cacher-go, a caching proxy server for Debian/Ubuntu package repositories.
// It provides commands to start the server and run benchmarks.
// It also handles configuration loading from various sources, including command-line flags and configuration files.
// It uses the Cobra library for command-line parsing and Viper for configuration management.
// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"fmt"
	"os"

	"github.com/jdfalk/apt-cacher-go/cmd/benchmark"
	"github.com/jdfalk/apt-cacher-go/cmd/serve"
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
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}
}
