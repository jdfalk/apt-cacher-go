package server

import (
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
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

// setupHTTPSServer configures HTTPS server if enabled
func (s *Server) setupHTTPSServer(mainMux *http.ServeMux) {
	if s.cfg.TLSEnabled && s.cfg.TLSCert != "" && s.cfg.TLSKey != "" {
		tlsConfig := &tls.Config{
			MinVersion: tls.VersionTLS12,
			CipherSuites: []uint16{
				tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
				tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
				tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			},
		}

		s.httpsServer = &http.Server{
			Addr:      fmt.Sprintf("%s:%d", s.cfg.ListenAddress, s.cfg.TLSPort),
			Handler:   s.acl.Middleware(mainMux),
			TLSConfig: tlsConfig,
		}
	}
}

// Add this new method to handle CONNECT requests
func (s *Server) handleConnectRequest(w http.ResponseWriter, r *http.Request) {
	// Only accept CONNECT method
	if r.Method != http.MethodConnect {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check if the host is a known keyserver
	host := r.Host
	isKeyserver := false
	knownKeyservers := []string{
		"keyserver.ubuntu.com",
		"keys.gnupg.net",
		"pool.sks-keyservers.net",
		"hkps.pool.sks-keyservers.net",
		"keys.openpgp.org",
	}

	for _, ks := range knownKeyservers {
		if strings.Contains(host, ks) {
			isKeyserver = true
			break
		}
	}

	// Log the connection attempt
	if isKeyserver {
		log.Printf("Tunneling connection to keyserver: %s", host)
	} else {
		log.Printf("Tunneling connection to: %s", host)
	}

	// Get the underlying connection
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "Hijacking not supported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	// Connect to the remote host
	remoteConn, err := net.DialTimeout("tcp", r.Host, 10*time.Second)
	if err != nil {
		_, writeErr := clientConn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		if writeErr != nil {
			log.Printf("Error writing to client: %v", writeErr)
		}
		clientConn.Close()
		return
	}

	// Tell the client everything is OK
	_, err = clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	if err != nil {
		log.Printf("Error writing to client: %v", err)
		clientConn.Close()
		remoteConn.Close()
		return
	}

	// Start copying data back and forth
	go func() {
		defer clientConn.Close()
		defer remoteConn.Close()
		_, err := io.Copy(remoteConn, clientConn)
		if err != nil && err != io.EOF {
			log.Printf("Error copying from client to remote: %v", err)
		}
	}()

	go func() {
		defer clientConn.Close()
		defer remoteConn.Close()
		_, err := io.Copy(clientConn, remoteConn)
		if err != nil && err != io.EOF {
			log.Printf("Error copying from remote to client: %v", err)
		}
	}()
}
