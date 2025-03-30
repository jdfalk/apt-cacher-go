package keymanager

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/jdfalk/apt-cacher-go/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewKeyManager tests the creation of a new key manager
func TestNewKeyManager(t *testing.T) {
	// Create temp directory
	tempDir, err := os.MkdirTemp("", "keymanager-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create key directory
	keyDir := filepath.Join(tempDir, "keys")

	// Test creation with enabled config
	cfg := &config.KeyManagementConfig{
		Enabled:      true,
		AutoRetrieve: true,
		KeyTTL:       "365d",
		Keyservers:   []string{"hkp://keyserver.ubuntu.com"},
		KeyDir:       keyDir,
	}

	km, err := New(cfg, tempDir)
	require.NoError(t, err)
	require.NotNil(t, km)
	assert.Equal(t, keyDir, km.config.KeyDir)

	// Test creation with disabled config
	disabledCfg := &config.KeyManagementConfig{
		Enabled: false,
	}

	km2, err := New(disabledCfg, tempDir)
	require.NoError(t, err)
	assert.Nil(t, km2)
}

// TestDetectKeyError tests the key error detection functionality
func TestDetectKeyError(t *testing.T) {
	// Create temp directory
	tempDir, err := os.MkdirTemp("", "keymanager-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create key directory
	keyDir := filepath.Join(tempDir, "keys")

	// Create key manager
	cfg := &config.KeyManagementConfig{
		Enabled:      true,
		AutoRetrieve: true,
		KeyDir:       keyDir,
	}

	km, err := New(cfg, tempDir)
	require.NoError(t, err)

	// Test error detection
	testCases := []struct {
		content       string
		expectedKeyID string
		shouldDetect  bool
	}{
		{
			content:       "The following signatures couldn't be verified because the public key is not available: NO_PUBKEY 648ACFD622F3D138",
			expectedKeyID: "648ACFD622F3D138",
			shouldDetect:  true,
		},
		{
			content:       "The following signatures were invalid: KEYEXPIRED 1234567890ABCDEF",
			expectedKeyID: "1234567890ABCDEF",
			shouldDetect:  true,
		},
		{
			content:       "Invalid signature: REVKEYSIG FEDCBA0987654321",
			expectedKeyID: "FEDCBA0987654321",
			shouldDetect:  true,
		},
		{
			content:       "Regular content with no key errors",
			expectedKeyID: "",
			shouldDetect:  false,
		},
	}

	for _, tc := range testCases {
		keyID, detected := km.DetectKeyError([]byte(tc.content))
		assert.Equal(t, tc.shouldDetect, detected)
		assert.Equal(t, tc.expectedKeyID, keyID)
	}
}

// TestKeyPathOperations tests key path handling and existence checks
func TestKeyPathOperations(t *testing.T) {
	// Create temp directory
	tempDir, err := os.MkdirTemp("", "keymanager-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create key directory
	keyDir := filepath.Join(tempDir, "keys")
	require.NoError(t, os.MkdirAll(keyDir, 0755))

	// Create key manager
	cfg := &config.KeyManagementConfig{
		Enabled:      true,
		AutoRetrieve: true,
		KeyDir:       keyDir,
	}

	km, err := New(cfg, tempDir)
	require.NoError(t, err)

	// Test GetKeyPath
	keyID := "648ACFD622F3D138"
	expectedPath := filepath.Join(keyDir, keyID+".gpg")

	assert.Equal(t, expectedPath, km.GetKeyPath(keyID))

	// Test HasKey (should be false initially)
	assert.False(t, km.HasKey(keyID))

	// Create a mock key file
	keyFile := filepath.Join(keyDir, keyID+".gpg")
	require.NoError(t, os.WriteFile(keyFile, []byte("mock key data"), 0644))

	// Recreate the key manager to detect existing files
	km, err = New(cfg, tempDir)
	require.NoError(t, err)

	// Test HasKey again (should be true now)
	assert.True(t, km.HasKey(keyID))
}

// TestFetchKey tests retrieving a key from a keyserver
func TestFetchKey(t *testing.T) {
	// Create a mock keyserver
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check the request URL
		if r.URL.Path == "/pks/lookup" {
			// Check the query parameters
			query := r.URL.Query()
			if query.Get("op") == "get" && query.Get("search") == "0x12345678" {
				// Return a mock key
				w.WriteHeader(http.StatusOK)
				_, err := w.Write([]byte("-----BEGIN PGP PUBLIC KEY BLOCK-----\nMock Key Data\n-----END PGP PUBLIC KEY BLOCK-----"))
				if err != nil {
					t.Fatalf("failed to write response: %v", err)
				}
				return
			}
		}

		// Otherwise return 404
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockServer.Close()

	// Create temp directory
	tempDir, err := os.MkdirTemp("", "keymanager-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create key directory
	keyDir := filepath.Join(tempDir, "keys")
	require.NoError(t, os.MkdirAll(keyDir, 0755))

	// Create key manager with our mock server
	serverURL := mockServer.URL
	serverURL = "hkp://" + serverURL[7:] // Replace http:// with hkp://

	cfg := &config.KeyManagementConfig{
		Enabled:      true,
		AutoRetrieve: true,
		KeyTTL:       "365d",
		Keyservers:   []string{serverURL},
		KeyDir:       keyDir,
	}

	km, err := New(cfg, tempDir)
	require.NoError(t, err)

	// Test fetching a key
	err = km.FetchKey("12345678")
	require.NoError(t, err)

	// Verify the key was saved
	keyPath := filepath.Join(keyDir, "12345678.gpg")
	assert.FileExists(t, keyPath)

	// Verify key is in cache
	assert.True(t, km.HasKey("12345678"))

	// Test fetching a non-existent key
	err = km.FetchKey("nonexistent")
	assert.Error(t, err)
}

// TestKeyManagerWithExistingKeys tests loading existing keys at initialization
func TestKeyManagerWithExistingKeys(t *testing.T) {
	// Create temp directory
	tempDir, err := os.MkdirTemp("", "keymanager-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create key directory
	keyDir := filepath.Join(tempDir, "keys")
	require.NoError(t, os.MkdirAll(keyDir, 0755))

	// Create mock key files
	key1ID := "key1"
	key2ID := "key2"
	require.NoError(t, os.WriteFile(filepath.Join(keyDir, key1ID+".gpg"), []byte("key1 data"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(keyDir, key2ID+".gpg"), []byte("key2 data"), 0644))

	// Create key manager
	cfg := &config.KeyManagementConfig{
		Enabled:      true,
		AutoRetrieve: true,
		KeyDir:       keyDir,
	}

	km, err := New(cfg, tempDir)
	require.NoError(t, err)

	// Key cache should be populated with existing keys
	assert.True(t, km.HasKey(key1ID))
	assert.True(t, km.HasKey(key2ID))
	assert.False(t, km.HasKey("nonexistent"))
}
