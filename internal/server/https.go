package server

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
)

// handleHTTPSRequest handles HTTPS repository requests by remapping them to HTTP
func (s *Server) handleHTTPSRequest(w http.ResponseWriter, r *http.Request) {
	// Get the original host from the request
	originalHost := r.Host
	if originalHost == "" {
		http.Error(w, "Missing Host header", http.StatusBadRequest)
		return
	}

	// Extract the path from the request
	originalPath := r.URL.Path

	// Log the HTTPS request
	log.Printf("HTTPS request: %s%s", originalHost, originalPath)

	// Check if this is a known repository host that should be remapped
	repoName, shouldRemap := s.shouldRemapHost(originalHost)
	if !shouldRemap {
		log.Printf("Unknown HTTPS host: %s, passing through", originalHost)
		http.Error(w, "Repository not configured for caching", http.StatusNotFound)
		return
	}

	// Construct a new path based on repository mapping
	newPath := fmt.Sprintf("/%s%s", repoName, originalPath)

	// Log the remapping
	log.Printf("Remapping HTTPS request to: %s", newPath)

	// Create a new request with the remapped path
	newReq := r.Clone(r.Context())
	newReq.URL.Path = newPath
	newReq.URL.Scheme = "http"
	newReq.Host = r.Host // Keep original host for request headers

	// Handle the remapped request using our normal package handler
	s.handlePackageRequest(w, newReq)
}

// shouldRemapHost determines if an HTTPS host should be remapped to an HTTP backend
func (s *Server) shouldRemapHost(host string) (string, bool) {
	// Remove port if present
	if idx := strings.IndexByte(host, ':'); idx >= 0 {
		host = host[:idx]
	}

	// Check against known repository hosts
	knownHosts := map[string]string{
		"download.docker.com":  "docker",
		"packages.grafana.com": "grafana",
		"downloads.plex.tv":    "plex",
		"apt.postgresql.org":   "postgresql",
		"hwraid.le-vert.net":   "hwraid",
		"deb.debian.org":       "debian",
		"security.debian.org":  "debian-security",
		"archive.ubuntu.com":   "ubuntu-archive",
		"security.ubuntu.com":  "ubuntu-security",
	}

	if repoName, found := knownHosts[host]; found {
		return repoName, true
	}

	// Check backends for more hosts
	for _, backend := range s.cfg.Backends {
		backendURL, err := url.Parse(backend.URL)
		if err != nil {
			continue
		}

		if backendURL.Host == host {
			return backend.Name, true
		}
	}

	return "", false
}
