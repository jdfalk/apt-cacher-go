package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// HealthCheckHandler returns a handler function for health checks
func HealthCheckHandler(isReady bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		status := "ok"
		if !isReady {
			status = "starting"
			w.WriteHeader(http.StatusServiceUnavailable)
		}

		response := map[string]string{
			"status": status,
		}

		if err := json.NewEncoder(w).Encode(response); err != nil {
			http.Error(w, "Failed to encode response", http.StatusInternalServerError)
			return
		}
	}
}

func TestHealthCheckHandler(t *testing.T) {
	// Test when server is ready
	t.Run("ready server", func(t *testing.T) {
		handler := HealthCheckHandler(true)

		req, err := http.NewRequest("GET", "/health", nil)
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		handler(rr, req)

		// Check status code
		assert.Equal(t, http.StatusOK, rr.Code)

		// Parse response
		var response map[string]string
		err = json.Unmarshal(rr.Body.Bytes(), &response)
		require.NoError(t, err)

		// Check response fields
		assert.Equal(t, "ok", response["status"])
	})

	// Test when server is not ready
	t.Run("starting server", func(t *testing.T) {
		handler := HealthCheckHandler(false)

		req, err := http.NewRequest("GET", "/health", nil)
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		handler(rr, req)

		// Check status code
		assert.Equal(t, http.StatusServiceUnavailable, rr.Code)

		// Parse response
		var response map[string]string
		err = json.Unmarshal(rr.Body.Bytes(), &response)
		require.NoError(t, err)

		// Check response fields
		assert.Equal(t, "starting", response["status"])
	})
}

// IMPORTANT: The documentation comment block below should not be removed unless
// the test itself is removed. Only modify the comment if the test's functionality
// changes. These comments are essential for understanding the test's purpose
// and approach, especially for future maintainers and code reviewers.

// TestHandleHealth tests the health check endpoint that provides system status information.
//
// The test verifies:
// - Basic health check returns correct status information
// - Detailed health check includes additional information
// - All required fields are present in the response
//
// Approach:
// 1. Creates a test server with the default configuration
// 2. Makes requests to the health endpoint with different parameters
// 3. Verifies that responses contain expected fields and status codes
// 4. Checks both basic and detailed health check responses
//
// Note: Uses httptest for HTTP request simulation
func TestHandleHealth(t *testing.T) {
	fixture := NewTestServerFixture(t)
	defer fixture.Cleanup()

	t.Run("basic_health_check", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/health", nil)
		w := httptest.NewRecorder()

		fixture.Server.HandleHealth(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

		var response map[string]interface{}
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)

		// Check required fields
		assert.Contains(t, response, "status")
		assert.Contains(t, response, "version")
		assert.Contains(t, response, "uptime")
		assert.Contains(t, response, "systemInfo")

		// Basic check shouldn't include detailed stats
		assert.NotContains(t, response, "cacheStats")
	})

	t.Run("detailed_health_check", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/health?detailed=true", nil)
		w := httptest.NewRecorder()

		fixture.Server.HandleHealth(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var response map[string]interface{}
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)

		// Detailed check should include additional stats
		assert.Contains(t, response, "cacheStats")
		assert.Contains(t, response, "backendStats")
	})
}

// IMPORTANT: The documentation comment block below should not be removed unless
// the test itself is removed. Only modify the comment if the test's functionality
// changes. These comments are essential for understanding the test's purpose
// and approach, especially for future maintainers and code reviewers.

// TestHandleReady tests the readiness check endpoint that indicates when the server
// is ready to accept connections.
//
// The test verifies:
// - Readiness endpoint returns correct status
// - Response format is valid JSON
// - Status code is 200 OK
//
// Approach:
// 1. Creates a test server instance
// 2. Makes a request to the ready endpoint
// 3. Verifies the response format and content
//
// Note: This is a simple test for a basic endpoint
func TestHandleReady(t *testing.T) {
	fixture := NewTestServerFixture(t)
	defer fixture.Cleanup()

	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()

	fixture.Server.HandleReady(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var response map[string]string
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, "ready", response["status"])
}
