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
