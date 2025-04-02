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

// IMPORTANT: The documentation comment block below should not be removed unless
// the test itself is removed. Only modify the comment if the test's functionality
// changes. These comments are essential for understanding the test's purpose
// and approach, especially for future maintainers and code reviewers.

// TestAdminHandler tests the basic functionality of the admin handler.
//
// The test verifies:
// - The handler returns a 200 OK status code
// - The handler returns the expected response body
// - The content is properly written to the response
//
// Approach:
// 1. Creates a mock HTTP request to the "/admin" endpoint
// 2. Creates a response recorder to capture the handler's response
// 3. Calls the handler with the request and response recorder
// 4. Verifies the status code and response body match expected values
//
// Note: This test focuses solely on the handler behavior without middleware or authentication
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

// IMPORTANT: The documentation comment block below should not be removed unless
// the test itself is removed. Only modify the comment if the test's functionality
// changes. These comments are essential for understanding the test's purpose
// and approach, especially for future maintainers and code reviewers.

// TestBasicAuthMiddleware tests the authentication middleware used to protect admin routes.
//
// The test verifies:
// - Requests with no authentication are properly rejected with 401 Unauthorized
// - Requests with correct credentials are allowed to proceed to the handler
// - Requests with incorrect credentials are rejected with 401 Unauthorized
// - The WWW-Authenticate header is properly set for authentication failures
//
// Approach:
//  1. Creates a simple middleware function that implements basic auth checking
//  2. Creates a test handler that returns "Authorized" on successful auth
//  3. Applies the middleware to the test handler
//  4. Tests three scenarios using subtests:
//     a. No authentication provided
//     b. Correct username/password
//     c. Incorrect username/password
//  5. Verifies the response status code and body for each scenario
//
// Note: Uses separate subtests for each authentication scenario to clearly
// isolate and identify different authentication behaviors
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
		assert.Contains(t, rr.Header().Get("WWW-Authenticate"), "Basic")
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
		assert.Contains(t, rr.Header().Get("WWW-Authenticate"), "Basic")
	})
}
