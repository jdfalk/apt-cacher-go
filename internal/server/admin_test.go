package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// AdminHandler returns a handler for admin operations
func AdminHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Simple handler for testing
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte("Admin operation successful"))
		if err != nil {
			http.Error(w, "Failed to write response", http.StatusInternalServerError)
		}
	}
}

func TestAdminHandler(t *testing.T) {
	// Create a test request
	req, _ := http.NewRequest("GET", "/admin", nil)
	rr := httptest.NewRecorder()

	// Call the handler
	handler := AdminHandler()
	handler.ServeHTTP(rr, req)

	// Check the response
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "Admin operation successful", rr.Body.String())
}

func TestBasicAuthMiddleware(t *testing.T) {
	// Create the middleware
	middleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			username, password, ok := r.BasicAuth()

			if !ok || username != "admin" || password != "password" {
				w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}

	// Create test handler
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte("Authorized"))
		if err != nil {
			http.Error(w, "Failed to write response", http.StatusInternalServerError)
		}
	})

	// Apply middleware
	handler := middleware(testHandler)

	// Test without auth
	t.Run("no auth", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/admin", nil)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusUnauthorized, rr.Code)
	})

	// Test with correct auth
	t.Run("correct auth", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/admin", nil)
		req.SetBasicAuth("admin", "password")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		assert.Equal(t, "Authorized", rr.Body.String())
	})

	// Test with incorrect auth
	t.Run("incorrect auth", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/admin", nil)
		req.SetBasicAuth("wrong", "wrong")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusUnauthorized, rr.Code)
	})
}
