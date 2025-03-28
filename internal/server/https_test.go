package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/jdfalk/apt-cacher-go/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPSConnection(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://example.com", nil)
	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}
}

func TestShouldRemapHost(t *testing.T) {
	// Create a server with some known backends
	cfg := &config.Config{
		Backends: []config.Backend{
			{
				Name: "debian",
				URL:  "http://deb.debian.org",
			},
			{
				Name: "ubuntu",
				URL:  "http://archive.ubuntu.com",
			},
			{
				Name: "custom-repo",
				URL:  "http://example.org/repo",
			},
		},
	}

	server := &Server{cfg: cfg}

	testCases := []struct {
		host             string
		expectedRepo     string
		shouldBeRemapped bool
	}{
		// Known hosts in map
		{"download.docker.com", "docker", true},
		{"packages.grafana.com", "grafana", true},
		{"apt.postgresql.org", "postgresql", true},
		{"deb.debian.org", "debian", true},
		{"archive.ubuntu.com", "ubuntu-archive", true},

		// Host with port
		{"download.docker.com:443", "docker", true},

		// Hosts from backend config
		{"example.org", "custom-repo", true},

		// Unknown hosts
		{"unknown.example.com", "", false},
		{"randomhost.org", "", false},
	}

	for _, tc := range testCases {
		t.Run(tc.host, func(t *testing.T) {
			repo, shouldRemap := server.shouldRemapHost(tc.host)
			assert.Equal(t, tc.shouldBeRemapped, shouldRemap)
			if tc.shouldBeRemapped {
				assert.Equal(t, tc.expectedRepo, repo)
			} else {
				assert.Empty(t, repo)
			}
		})
	}
}

func TestHTTPSRequestHandling(t *testing.T) {
	// Create a test server that we can inspect
	server, _, cleanup := createTestServer(t)
	defer cleanup()

	// Add a custom handler that will record the request seen
	var capturedPath string

	// Create a test-specific handler
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})

	// Create a custom HTTPS handler for testing
	testHTTPSHandler := func(w http.ResponseWriter, r *http.Request) {
		repo, shouldRemap := server.shouldRemapHost(r.Host)
		if shouldRemap {
			r.URL.Path = "/" + repo + r.URL.Path
			testHandler.ServeHTTP(w, r)
		} else if r.Host == "" {
			http.Error(w, "Missing host in request", http.StatusBadRequest)
		} else {
			http.Error(w, "Unknown host: "+r.Host, http.StatusNotFound)
		}
	}

	// Use the custom handler directly in the test cases

	t.Run("valid https remap", func(t *testing.T) {
		// Create a request to a known host
		req := httptest.NewRequest("GET", "https://download.docker.com/linux/debian/dists/bullseye/stable/binary-amd64/Packages", nil)
		req.Host = "download.docker.com"

		// Execute the handler
		w := httptest.NewRecorder()
		server.handleHTTPSRequest(w, req)

		// Verify it was remapped correctly
		assert.Equal(t, "/docker/linux/debian/dists/bullseye/stable/binary-amd64/Packages", capturedPath)

		// Execute the handler
		w = httptest.NewRecorder()
		testHTTPSHandler(w, req)
	})

	t.Run("unknown host", func(t *testing.T) {
		// Create a request to an unknown host
		req := httptest.NewRequest("GET", "https://unknown.example.com/some/path", nil)
		req.Host = "unknown.example.com"

		// Execute the handler
		w := httptest.NewRecorder()
		server.handleHTTPSRequest(w, req)

		// Should get 404 not found
		assert.Equal(t, http.StatusNotFound, w.Code)

		// Execute the handler
		w = httptest.NewRecorder()
		testHTTPSHandler(w, req)

		// Should get 404 not found
		req.Host = ""

		// Execute the handler
		w = httptest.NewRecorder()
		server.handleHTTPSRequest(w, req)

		// Should get 400 bad request
		assert.Equal(t, http.StatusBadRequest, w.Code)

		// Execute the handler
		w = httptest.NewRecorder()
		testHTTPSHandler(w, req)

		// Should get 400 bad request
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("url parsing tests", func(t *testing.T) {
		validURL := "http://archive.ubuntu.com/ubuntu"
		parsedURL, err := url.Parse(validURL)
		require.NoError(t, err)
		assert.Equal(t, "archive.ubuntu.com", parsedURL.Host)

		// Test with HTTPS
		httpsURL := "https://packages.grafana.com/oss/deb"
		parsedHTTPS, err := url.Parse(httpsURL)
		require.NoError(t, err)
		assert.Equal(t, "packages.grafana.com", parsedHTTPS.Host)

		// Test with port
		urlWithPort := "http://localhost:8080/repo"
		parsedWithPort, err := url.Parse(urlWithPort)
		require.NoError(t, err)
		assert.Equal(t, "localhost:8080", parsedWithPort.Host)
	})
}
