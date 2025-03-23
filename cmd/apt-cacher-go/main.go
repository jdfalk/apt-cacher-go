package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/jdfalk/apt-cacher-go/internal/config"
	"github.com/jdfalk/apt-cacher-go/internal/server"
)

func main() {
	configPath := flag.String("config", "/etc/apt-cacher-go/config.yaml", "Path to configuration file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	srv, err := server.New(cfg)
	if err != nil {
		log.Fatalf("Failed to initialize server: %v", err)
	}

	go func() {
		if err := srv.Start(); err != nil {
			log.Fatalf("Server error: %v", err)
		}
	}()

	fmt.Printf("apt-cacher-go started on %s\n", cfg.ListenAddress)

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	fmt.Println("Shutting down...")
	if err := srv.Shutdown(); err != nil {
		log.Fatalf("Server shutdown error: %v", err)
	}
}
