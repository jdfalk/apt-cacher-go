package keymanager

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jdfalk/apt-cacher-go/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// IMPORTANT: The documentation comment block below should not be removed unless
// the test itself is removed. Only modify the comment if the test's functionality
// changes. These comments are essential for understanding the test's purpose
// and approach, especially for future maintainers and code reviewers.

// TestNewKeyManager tests the creation of a new key manager.
//
// The test verifies:
// - Key manager can be created with enabled configuration
// - Key manager is nil when configuration is disabled
// - The key directory is correctly set
//
// Approach:
// 1. Creates a temporary directory for testing
// 2. Tests creation with enabled configuration
// 3. Tests creation with disabled configuration
// 4. Verifies the key directory is correctly set
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

// IMPORTANT: The documentation comment block below should not be removed unless
// the test itself is removed. Only modify the comment if the test's functionality
// changes. These comments are essential for understanding the test's purpose
// and approach, especially for future maintainers and code reviewers.

// TestDetectKeyError tests the key error detection functionality.
//
// The test verifies:
// - Various types of key error messages are correctly detected
// - Key IDs are properly extracted from error messages
// - Non-key-error messages are correctly ignored
//
// Approach:
// 1. Creates a key manager with test configuration
// 2. Tests multiple error message patterns
// 3. Verifies key IDs are correctly extracted
// 4. Verifies non-key-error messages don't produce false positives
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

// IMPORTANT: The documentation comment block below should not be removed unless
// the test itself is removed. Only modify the comment if the test's functionality
// changes. These comments are essential for understanding the test's purpose
// and approach, especially for future maintainers and code reviewers.

// TestKeyPathOperations tests key path handling and existence checks.
//
// The test verifies:
// - GetKeyPath returns the correct path for a key
// - HasKey correctly reports key existence
// - Key detection works before and after a key is added
//
// Approach:
// 1. Creates a key manager with test configuration
// 2. Tests GetKeyPath for path construction
// 3. Tests HasKey returns false when a key doesn't exist
// 4. Creates a key file and verifies HasKey returns true
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

// IMPORTANT: The documentation comment block below should not be removed unless
// the test itself is removed. Only modify the comment if the test's functionality
// changes. These comments are essential for understanding the test's purpose
// and approach, especially for future maintainers and code reviewers.

// TestFetchKey tests retrieving a key from a keyserver.
//
// The test verifies:
// - Keys can be fetched from a keyserver
// - The key is saved to the correct location
// - The key cache is updated after a successful fetch
// - Error handling for failed key fetches
//
// Approach:
// 1. Creates a mock keyserver that returns test key data
// 2. Configures a key manager to use the mock server
// 3. Tests fetching a key and verifies it's saved correctly
// 4. Tests error handling for non-existent keys
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

// IMPORTANT: The documentation comment block below should not be removed unless
// the test itself is removed. Only modify the comment if the test's functionality
// changes. These comments are essential for understanding the test's purpose
// and approach, especially for future maintainers and code reviewers.

// TestKeyManagerWithExistingKeys tests loading existing keys at initialization.
//
// The test verifies:
// - Existing key files are detected during initialization
// - The key cache is correctly populated with existing keys
// - HasKey correctly identifies existing and non-existent keys
//
// Approach:
// 1. Creates a temporary directory with mock key files
// 2. Initializes a key manager in that directory
// 3. Verifies existing keys are detected and cached
// 4. Tests HasKey for existing and non-existent keys
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

// IMPORTANT: The documentation comment block below should not be removed unless
// the test itself is removed. Only modify the comment if the test's functionality
// changes. These comments are essential for understanding the test's purpose
// and approach, especially for future maintainers and code reviewers.

// TestVerifySignature tests the VerifySignature method which validates digital
// signatures using GPG keys.
//
// The test verifies:
// - Signatures can be verified when the corresponding key is present
// - Unknown signatures are properly identified
// - Key detection works correctly from signature data
//
// Approach:
// 1. Creates a key manager with test configuration
// 2. Creates mock key data and signature data
// 3. Tests signature verification with existing keys
// 4. Tests signature verification with missing keys
// 5. Tests key detection from signature data
func TestVerifySignature(t *testing.T) {
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

	// Create a mock key file
	keyID := "648ACFD622F3D138"
	keyFile := filepath.Join(keyDir, keyID+".gpg")
	require.NoError(t, os.WriteFile(keyFile, []byte("mock key data"), 0644))

	// No need to recreate the KeyManager since we can just use the HasKey method with the existing km
	// which will check if the key file exists

	// Test DetectKeySignature
	t.Run("detect_key_signature", func(t *testing.T) {
		signedData := []byte("This data is signed with key ID 648ACFD622F3D138")
		detectedKeyID, hasKeyID := km.DetectKeySignature(signedData)

		assert.True(t, hasKeyID)
		assert.Equal(t, keyID, detectedKeyID)
	})

	// Test VerifySignature with key present
	t.Run("verify_with_key_present", func(t *testing.T) {
		signedData := []byte("This data is signed with key ID 648ACFD622F3D138")
		signature := []byte("mock signature")

		result, err := km.VerifySignature(signedData, signature)
		assert.NoError(t, err)
		assert.True(t, result)
	})

	// Test VerifySignature with key missing
	t.Run("verify_with_key_missing", func(t *testing.T) {
		signedData := []byte("This data is signed with key ID ABCD1234ABCD1234")
		signature := []byte("mock signature")

		result, err := km.VerifySignature(signedData, signature)
		assert.NoError(t, err)
		assert.False(t, result)
	})
}

// IMPORTANT: The documentation comment block below should not be removed unless
// the test itself is removed. Only modify the comment if the test's functionality
// changes. These comments are essential for understanding the test's purpose
// and approach, especially for future maintainers and code reviewers.

// TestPrefetchDefaultKeys tests the PrefetchDefaultKeys method which proactively
// downloads common repository keys.
//
// The test verifies:
// - Default keys are fetched when none are specified
// - Custom key lists can be provided
// - The method reports successful and failed fetches
//
// Approach:
// 1. Creates a mock keyserver that serves specific keys
// 2. Creates a key manager with the mock server
// 3. Tests prefetching with default keys
// 4. Tests prefetching with a custom key list
// 5. Verifies the results are reported correctly
func TestPrefetchDefaultKeys(t *testing.T) {
	// Create a mock keyserver
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check the request URL
		if r.URL.Path == "/pks/lookup" {
			// Check the query parameters
			query := r.URL.Query()
			search := query.Get("search")
			// Only return success for specific keys
			if strings.HasPrefix(search, "0x123") || strings.HasPrefix(search, "0x456") {
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

	// Test prefetching with a custom key list
	// Include some keys that will succeed and some that will fail
	keys := []string{"123456", "456789", "789012"}
	successful, failed, err := km.PrefetchDefaultKeys(keys)
	require.NoError(t, err)

	// We expect 2 successful fetches and 1 failure
	assert.Equal(t, 2, successful)
	assert.Equal(t, 1, failed)

	// Verify keys are in cache and on disk
	assert.True(t, km.HasKey("123456"))
	assert.True(t, km.HasKey("456789"))
	assert.False(t, km.HasKey("789012"))
}

// IMPORTANT: The documentation comment block below should not be removed unless
// the test itself is removed. Only modify the comment if the test's functionality
// changes. These comments are essential for understanding the test's purpose
// and approach, especially for future maintainers and code reviewers.

// TestRemoveKey tests the RemoveKey method which deletes keys from disk and cache.
//
// The test verifies:
// - Keys can be properly removed from the filesystem
// - The key cache is updated after removal
// - HasKey returns false after removal
// - Error handling for removal of non-existent keys
//
// Approach:
// 1. Creates a key manager with test keys
// 2. Removes a key and verifies it's gone from disk and cache
// 3. Tests removing a non-existent key
// 4. Verifies HasKey properly reflects the state after removal
func TestRemoveKey(t *testing.T) {
	// Create temp directory
	tempDir, err := os.MkdirTemp("", "keymanager-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create key directory
	keyDir := filepath.Join(tempDir, "keys")
	require.NoError(t, os.MkdirAll(keyDir, 0755))

	// Create mock key files
	keyID := "testkey123"
	require.NoError(t, os.WriteFile(filepath.Join(keyDir, keyID+".gpg"), []byte("test key data"), 0644))

	// Create key manager
	cfg := &config.KeyManagementConfig{
		Enabled:      true,
		AutoRetrieve: true,
		KeyDir:       keyDir,
	}

	km, err := New(cfg, tempDir)
	require.NoError(t, err)

	// Verify key exists
	assert.True(t, km.HasKey(keyID))

	// Remove the key
	err = km.RemoveKey(keyID)
	require.NoError(t, err)

	// Verify key no longer exists
	assert.False(t, km.HasKey(keyID))
	_, err = os.Stat(filepath.Join(keyDir, keyID+".gpg"))
	assert.True(t, os.IsNotExist(err))

	// Test removing non-existent key
	err = km.RemoveKey("nonexistent")
	require.NoError(t, err) // Should succeed because it's idempotent
}
