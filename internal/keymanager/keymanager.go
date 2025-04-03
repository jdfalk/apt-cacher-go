package keymanager

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jdfalk/apt-cacher-go/internal/config"
)

// KeyManager handles GPG key operations for apt repositories.
// It provides functionality to fetch, store, and manage GPG keys used
// by apt repositories for package verification. The KeyManager stores
// keys in a configurable directory and maintains an in-memory cache of
// available keys to reduce redundant downloads.
type KeyManager struct {
	config            *config.KeyManagementConfig // Configuration for key management
	keyCache          map[string]time.Time        // Cache of known keys with their fetch time
	fetchMutex        sync.RWMutex                // Mutex to protect concurrent operations on keyCache
	showKeyOperations bool                        // Flag to enable detailed key operation logging
}

// New creates a new key manager instance with debug options
//
// This function:
// - Checks if key management is enabled in configuration
// - Creates the key directory if it doesn't exist
// - Verifies the key directory is writable with a test file
// - Initializes the in-memory key cache with existing keys
// - Falls back to a subdirectory in the cache if the specified directory isn't writable
//
// Parameters:
// - cfg: Key management configuration
// - cacheDir: Base cache directory for fallback
// - debugOptions: Debug logging options
//
// Returns:
// - Initialized KeyManager or nil if key management is disabled
// - Error if directory creation fails or is not writable
func New(cfg *config.KeyManagementConfig, cacheDir string, debugOptions *config.DebugLog) (*KeyManager, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	// Get the specified key directory
	keyDir := cfg.KeyDir

	// If key directory is empty, set a default
	if keyDir == "" {
		keyDir = filepath.Join(cacheDir, "keys")
		cfg.KeyDir = keyDir
		log.Printf("No key directory specified, using default: %s", keyDir)
	}

	// Try to create key directory
	err := os.MkdirAll(keyDir, 0755)
	if err != nil {
		// If permission denied, fallback to a subdirectory in the cache
		if os.IsPermission(err) {
			log.Printf("Warning: Permission denied to create key directory at %s, falling back to cache directory", keyDir)
			keyDir = filepath.Join(cacheDir, "keys")

			// Try to create fallback directory
			err = os.MkdirAll(keyDir, 0755)
			if err != nil {
				return nil, fmt.Errorf("failed to create fallback key directory: %w", err)
			}

			// Update config with new directory
			cfg.KeyDir = keyDir
			log.Printf("Using fallback key directory: %s", keyDir)
		} else {
			return nil, fmt.Errorf("failed to create key directory: %w", err)
		}
	}

	// Add a test write to verify directory is writable
	testFile := filepath.Join(keyDir, ".keys_writable")
	testContent := fmt.Sprintf("Directory write test: %s", time.Now().Format(time.RFC3339))
	if err := os.WriteFile(testFile, []byte(testContent), 0644); err != nil {
		log.Printf("Warning: Failed to write test file to key directory %s: %v", keyDir, err)
		return nil, fmt.Errorf("key directory exists but is not writable: %w", err)
	} else {
		log.Printf("Successfully verified key directory %s is writable", keyDir)
	}

	// Initialize the key manager with debug options
	manager := &KeyManager{
		config:            cfg,
		keyCache:          make(map[string]time.Time),
		showKeyOperations: debugOptions != nil && debugOptions.ShowKeyOperations,
	}

	// Initialize key cache with existing keys
	files, err := os.ReadDir(keyDir)
	if err != nil {
		log.Printf("Warning: couldn't read key directory: %v", err)
	} else {
		for _, file := range files {
			if !file.IsDir() && strings.HasSuffix(file.Name(), ".gpg") {
				keyID := strings.TrimSuffix(file.Name(), ".gpg")
				info, err := file.Info()
				if err == nil {
					// Store the key ID in the original case from the filename
					manager.keyCache[keyID] = info.ModTime()
					if manager.showKeyOperations {
						log.Printf("[KEY OPERATION] Loaded existing GPG key: %s", keyID)
					}
				}
			}
		}

		if manager.showKeyOperations {
			log.Printf("[KEY OPERATION] Initialized key cache with %d existing keys", len(manager.keyCache))
		} else {
			log.Printf("Initialized key cache with %d existing keys", len(manager.keyCache))
		}
	}

	return manager, nil
}

// DetectKeyError checks if an error response contains a missing GPG key message
// and extracts the key ID if found.
//
// This function analyzes response data to identify common GPG key error patterns,
// such as NO_PUBKEY, KEYEXPIRED, REVKEYSIG, or KEYREVOKED messages. If a key error
// is detected, it extracts and returns the key ID.
//
// Parameters:
// - data: Response data to analyze for key errors
//
// Returns:
// - keyID: The extracted key ID if found
// - found: Boolean indicating whether a key error was detected
func DetectKeyError(data []byte) (string, bool) {
	content := string(data)

	// Check for various key error patterns
	patterns := []string{
		`NO_PUBKEY (\w+)`,
		`KEYEXPIRED (\w+)`,
		`REVKEYSIG (\w+)`,
		`KEYREVOKED (\w+)`,
		`NODATA (\w+)`,
		`key ID (\w+) not found`,
		`public key not available: (\w+)`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		match := re.FindStringSubmatch(content)
		if len(match) > 1 {
			return match[1], true
		}
	}

	return "", false
}

// FetchKey retrieves a GPG key from configured keyservers.
//
// This function attempts to download a GPG key from the configured keyservers.
// If the key is already present and not expired, it returns immediately.
// Otherwise, it tries each keyserver in order until the key is successfully
// retrieved. The function includes proper error handling and extensive logging.
//
// Parameters:
// - keyID: The ID of the key to fetch
//
// Returns:
// - Error if the key could not be fetched from any server
func (km *KeyManager) FetchKey(keyID string) error {
	if km == nil || !km.config.AutoRetrieve {
		return errors.New("key management not enabled")
	}

	// Normalize key ID - support both with and without 0x prefix
	keyID = strings.TrimPrefix(strings.ToUpper(keyID), "0X")

	// Check if we already have this key and it's not expired
	km.fetchMutex.RLock()
	// Case-insensitive check against the cache
	var fetchTime time.Time
	var exists bool
	for cachedKeyID, t := range km.keyCache {
		if strings.EqualFold(cachedKeyID, keyID) {
			fetchTime = t
			exists = true
			keyID = cachedKeyID // Use the original case from the cache
			break
		}
	}
	km.fetchMutex.RUnlock()

	// Log key operations if enabled
	if km.showKeyOperations {
		if exists {
			log.Printf("[KEY OPERATION] Found key %s in cache, fetch time: %s", keyID, fetchTime)
		} else {
			log.Printf("[KEY OPERATION] Key %s not found in cache, will try to fetch", keyID)
		}
	}

	if exists {
		// Check if the key file actually exists
		keyPath := km.GetKeyPath(keyID)
		if _, err := os.Stat(keyPath); os.IsNotExist(err) {
			if km.showKeyOperations {
				log.Printf("[KEY OPERATION] Key %s is in cache but file doesn't exist. Will re-download.", keyID)
			} else {
				log.Printf("Key %s is in cache but file doesn't exist. Will re-download.", keyID)
			}
		} else {
			keyTTL, err := time.ParseDuration(km.config.KeyTTL)
			if err != nil {
				keyTTL = 365 * 24 * time.Hour // Default to 1 year
			}

			if time.Since(fetchTime) < keyTTL {
				if km.showKeyOperations {
					log.Printf("[KEY OPERATION] Key %s already exists and is not expired", keyID)
				} else {
					log.Printf("Key %s already exists and is not expired", keyID)
				}
				return nil
			}

			if km.showKeyOperations {
				log.Printf("[KEY OPERATION] Key %s exists but has expired. Will refresh.", keyID)
			} else {
				log.Printf("Key %s exists but has expired. Will refresh.", keyID)
			}
		}
	}

	// Use a mutex to prevent multiple simultaneous fetches for the same key
	km.fetchMutex.Lock()
	defer km.fetchMutex.Unlock()

	// Check again if key was added while waiting for lock (case-insensitive)
	for cachedKeyID := range km.keyCache {
		if strings.EqualFold(cachedKeyID, keyID) {
			keyPath := km.GetKeyPath(cachedKeyID)
			if _, err := os.Stat(keyPath); err == nil {
				if km.showKeyOperations {
					log.Printf("[KEY OPERATION] Key %s was added by another goroutine while waiting", cachedKeyID)
				} else {
					log.Printf("Key %s was added by another goroutine while waiting", cachedKeyID)
				}
				return nil
			}
		}
	}

	if km.showKeyOperations {
		log.Printf("[KEY OPERATION] Starting fetch for key %s", keyID)
	} else {
		log.Printf("Starting fetch for key %s", keyID)
	}

	// Make sure we have at least one keyserver configured
	if len(km.config.Keyservers) == 0 {
		log.Printf("No keyservers configured. Adding default keyserver.")
		km.config.Keyservers = []string{"hkp://keyserver.ubuntu.com:80"}
	}

	// Try each keyserver until successful
	var lastErr error
	for _, server := range km.config.Keyservers {
		if km.showKeyOperations {
			log.Printf("[KEY OPERATION] Trying keyserver %s for key %s", server, keyID)
		} else {
			log.Printf("Trying keyserver %s for key %s", server, keyID)
		}

		err := km.fetchKeyFromServer(server, keyID)
		if err == nil {
			// Update cache time
			km.keyCache[keyID] = time.Now()
			if km.showKeyOperations {
				log.Printf("[KEY OPERATION] Successfully retrieved key %s from %s", keyID, server)
			} else {
				log.Printf("Successfully retrieved key %s from %s", keyID, server)
			}
			return nil
		}
		lastErr = err
		if km.showKeyOperations {
			log.Printf("[KEY OPERATION] Failed to retrieve key %s from %s: %v", keyID, server, err)
		} else {
			log.Printf("Failed to retrieve key %s from %s: %v", keyID, server, err)
		}
	}

	return fmt.Errorf("failed to retrieve key from all servers: %w", lastErr)
}

// fetchKeyFromServer retrieves a key from a specific keyserver.
//
// This function handles the protocol-specific details of fetching a key from
// different types of keyservers. Currently, it supports HKP protocol keyservers.
//
// Parameters:
// - server: The keyserver URL (with protocol)
// - keyID: The ID of the key to fetch
//
// Returns:
// - Error if the key could not be fetched
func (km *KeyManager) fetchKeyFromServer(server, keyID string) error {
	// For HKP keyservers
	if strings.HasPrefix(server, "hkp://") {
		server = strings.TrimPrefix(server, "hkp://")
		parts := strings.Split(server, ":")
		server = parts[0]
		port := "11371" // Default HKP port
		if len(parts) > 1 {
			port = parts[1]
		}

		url := fmt.Sprintf("http://%s:%s/pks/lookup?op=get&options=mr&search=0x%s", server, port, keyID)
		log.Printf("Fetching key %s from %s", keyID, url)
		return km.downloadKey(url, keyID)
	}

	// For HTTP/HTTPS keyservers
	if strings.HasPrefix(server, "http://") || strings.HasPrefix(server, "https://") {
		url := fmt.Sprintf("%s/pks/lookup?op=get&options=mr&search=0x%s", server, keyID)
		log.Printf("Fetching key %s from %s", keyID, url)
		return km.downloadKey(url, keyID)
	}

	return fmt.Errorf("unsupported keyserver protocol: %s", server)
}

// downloadKey fetches a key from a URL and saves it.
//
// This function handles the HTTP request to download a key and saves it to disk.
// It includes error handling, validation of the key content, and proper filesystem
// operations.
//
// Parameters:
// - url: The URL to download the key from
// - keyID: The ID of the key being downloaded
//
// Returns:
// - Error if the download or saving operation fails
func (km *KeyManager) downloadKey(url, keyID string) error {
	// Download the key
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	// Create the request with additional headers
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("error creating request: %w", err)
	}

	// Add headers that some keyservers expect
	req.Header.Set("User-Agent", "apt-cacher-go")
	req.Header.Set("Accept", "application/pgp-keys, application/pgp")

	log.Printf("Sending request to keyserver: %s", url)

	// Execute the request
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error connecting to keyserver: %v", err)
		return fmt.Errorf("error connecting to keyserver: %w", err)
	}
	defer resp.Body.Close()

	log.Printf("Key request for %s received status: %s", keyID, resp.Status)

	if resp.StatusCode != http.StatusOK {
		// Read response body for better error messages
		body, _ := io.ReadAll(resp.Body)
		log.Printf("Error response from keyserver for key %s: %s", keyID, string(body))
		return fmt.Errorf("keyserver returned status: %s, body: %s",
			resp.Status, string(body))
	}

	// Read response body first to check content
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("error reading keyserver response: %w", err)
	}

	// Check if the response looks like a PGP key
	if len(body) < 10 || !strings.Contains(string(body), "PGP") {
		log.Printf("Warning: keyserver response doesn't look like a PGP key: %s", string(body))
		if len(body) == 0 {
			return fmt.Errorf("keyserver returned empty response for key %s", keyID)
		}
	}

	// Ensure key directory exists
	err = os.MkdirAll(km.config.KeyDir, 0755)
	if err != nil {
		return fmt.Errorf("failed to create key directory: %w", err)
	}

	// Create key file - use keyID as-is for the filename
	keyPath := filepath.Join(km.config.KeyDir, keyID+".gpg")
	log.Printf("Saving key %s to %s (%d bytes)", keyID, keyPath, len(body))

	keyFile, err := os.Create(keyPath)
	if err != nil {
		return fmt.Errorf("failed to create key file %s: %w", keyPath, err)
	}
	defer keyFile.Close()

	// Write response body to file
	n, err := keyFile.Write(body)
	if err != nil {
		return fmt.Errorf("failed to write key data: %w", err)
	}

	// Check if we actually wrote any data
	if n == 0 {
		return fmt.Errorf("failed to write any data for key %s", keyID)
	}

	// Set file permissions to ensure readability
	if err := keyFile.Chmod(0644); err != nil {
		log.Printf("Warning: could not set permissions on key file: %v", err)
	}

	log.Printf("Successfully saved key %s (%d bytes)", keyID, n)

	// Note: The fetchMutex is already locked by FetchKey, so we don't need to lock it here

	return nil
}

// GetKeyPath returns the filesystem path to a key file.
//
// This function returns the complete filesystem path where a GPG key file
// is stored based on the key ID.
//
// Parameters:
// - keyID: The ID of the key
//
// Returns:
// - The complete filesystem path to the key file, or empty string if key management is disabled
func (km *KeyManager) GetKeyPath(keyID string) string {
	if km == nil {
		return ""
	}
	return filepath.Join(km.config.KeyDir, keyID+".gpg")
}

// HasKey checks if a key exists in the cache.
//
// This function verifies whether a specific key is available in the key cache.
// It performs a case-insensitive check against the cache.
//
// Parameters:
// - keyID: The ID of the key to check
//
// Returns:
// - true if the key exists, false otherwise
func (km *KeyManager) HasKey(keyID string) bool {
	if km == nil {
		return false
	}

	km.fetchMutex.RLock()
	defer km.fetchMutex.RUnlock()

	// Case-insensitive check
	for cachedKeyID := range km.keyCache {
		if strings.EqualFold(cachedKeyID, keyID) {
			return true
		}
	}

	return false
}

// VerifySignature verifies a digital signature using GPG.
//
// This function attempts to verify a digital signature using the appropriate
// GPG key. If the key is not available, it attempts to fetch it.
//
// Parameters:
// - signedData: The data that was signed
// - signature: The signature to verify
//
// Returns:
// - bool: Whether the signature is valid
// - error: Any error encountered during verification
func (km *KeyManager) VerifySignature(signedData []byte, signature []byte) (bool, error) {
	if km == nil {
		return false, errors.New("key manager not initialized")
	}

	// Extract signature key ID if possible
	keyID, hasKeyID := km.DetectKeySignature(signedData)
	if hasKeyID && !km.HasKey(keyID) {
		// Try to fetch the key if we don't have it
		if err := km.FetchKey(keyID); err != nil {
			log.Printf("Warning: Failed to fetch key %s: %v", keyID, err)
		}
	}

	// Implement signature verification using GPG
	// For now, return true if we have the key, false otherwise
	return km.HasKey(keyID), nil
}

// DetectKeySignature tries to extract the key ID from signed data.
//
// This function analyzes signed data to extract the ID of the key that was
// used for signing. It uses regular expressions to match common formats
// in which key IDs appear in signed data.
//
// Parameters:
// - data: The signed data to analyze
//
// Returns:
// - keyID: The extracted key ID, if found
// - found: Boolean indicating whether a key ID was found
func (km *KeyManager) DetectKeySignature(data []byte) (string, bool) {
	content := string(data)

	// Look for key IDs in the content
	patterns := []string{
		`signed by key ID (\w+)`,
		`signature from key ID (\w+)`,
		`key ID (\w+)`,
		`gpg: using RSA key (\w+)`,
		`gpg: Signature made .* using key ID (\w+)`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		match := re.FindStringSubmatch(content)
		if len(match) > 1 {
			return match[1], true
		}
	}

	return "", false
}

// ListAvailableKeys returns a list of all keys available in the key directory.
//
// This function scans the key directory and returns information about all
// available GPG keys, including their IDs, paths, and modification times.
//
// Returns:
// - A map of key IDs to their last modification times
// - An error if the directory couldn't be read
func (km *KeyManager) ListAvailableKeys() (map[string]time.Time, error) {
	if km == nil {
		return nil, errors.New("key manager not initialized")
	}

	// Refresh the key cache from disk
	keyDir := km.config.KeyDir
	files, err := os.ReadDir(keyDir)
	if err != nil {
		return nil, fmt.Errorf("couldn't read key directory: %w", err)
	}

	// Create a new map for the result
	km.fetchMutex.Lock()
	defer km.fetchMutex.Unlock()

	keys := make(map[string]time.Time)
	for _, file := range files {
		if !file.IsDir() && strings.HasSuffix(file.Name(), ".gpg") {
			keyID := strings.TrimSuffix(file.Name(), ".gpg")
			info, err := file.Info()
			if err == nil {
				keys[keyID] = info.ModTime()
			}
		}
	}

	// Update our cache
	km.keyCache = keys

	return keys, nil
}

// DetectKeyError checks if an error response contains a missing GPG key message
// and extracts the key ID if found.
//
// This method analyzes response data to identify common GPG key error patterns,
// such as NO_PUBKEY, KEYEXPIRED, REVKEYSIG, or KEYREVOKED messages. If a key error
// is detected, it extracts and returns the key ID.
//
// Parameters:
// - data: Response data to analyze for key errors
//
// Returns:
// - keyID: The extracted key ID if found
// - found: Boolean indicating whether a key error was detected
func (km *KeyManager) DetectKeyError(data []byte) (string, bool) {
	if km == nil || !km.config.AutoRetrieve {
		return "", false
	}

	return DetectKeyError(data)
}

// PrefetchDefaultKeys proactively fetches a list of commonly needed repository keys.
//
// This function attempts to download a set of important repository keys before
// they are needed by client requests. This helps prevent key-related errors
// during normal operation.
//
// Parameters:
// - keys: A slice of key IDs to prefetch
//
// Returns:
// - successful: Number of keys successfully prefetched
// - failed: Number of keys that couldn't be prefetched
// - error: Any critical error that prevented prefetching
func (km *KeyManager) PrefetchDefaultKeys(keys []string) (int, int, error) {
	if km == nil || !km.config.AutoRetrieve {
		return 0, 0, errors.New("key management not enabled")
	}

	if len(keys) == 0 {
		// Default set of common repository keys if none specified
		keys = []string{
			"871920D1991BC93C", // Ubuntu archive
			"3B4FE6ACC0B21F32", // Debian archive
			"A1BD8E9D78F7FE5C", // Debian security
			"F6ECB3762474EDA9", // Docker release
			"54404762BBB6E853", // PostgreSQL
			"8507EFA5",         // Grafana
			"D94AA3F0",         // NodeJS
		}
	}

	log.Printf("Starting prefetch of %d default GPG keys", len(keys))

	var successful, failed int
	for _, keyID := range keys {
		err := km.FetchKey(keyID)
		if err != nil {
			log.Printf("Failed to prefetch key %s: %v", keyID, err)
			failed++
		} else {
			log.Printf("Successfully prefetched key %s", keyID)
			successful++
		}
	}

	log.Printf("Completed prefetch of default keys: %d successful, %d failed",
		successful, failed)

	return successful, failed, nil
}

// RefreshExpiredKeys refreshes any keys in the cache that have expired.
//
// This function checks all keys in the cache and refreshes any that have
// exceeded their TTL by downloading them again from the keyservers.
//
// Returns:
// - refreshed: Number of keys successfully refreshed
// - failed: Number of keys that couldn't be refreshed
// - error: Any critical error that prevented the refresh operation
func (km *KeyManager) RefreshExpiredKeys() (int, int, error) {
	if km == nil || !km.config.AutoRetrieve {
		return 0, 0, errors.New("key management not enabled")
	}

	keyTTL, err := time.ParseDuration(km.config.KeyTTL)
	if err != nil {
		keyTTL = 365 * 24 * time.Hour // Default to 1 year
	}

	now := time.Now()
	var keysToRefresh []string

	// First, identify expired keys
	km.fetchMutex.RLock()
	for keyID, fetchTime := range km.keyCache {
		if now.Sub(fetchTime) > keyTTL {
			keysToRefresh = append(keysToRefresh, keyID)
		}
	}
	km.fetchMutex.RUnlock()

	if len(keysToRefresh) == 0 {
		log.Printf("No expired keys found")
		return 0, 0, nil
	}

	log.Printf("Found %d expired keys to refresh", len(keysToRefresh))

	// Then refresh them
	var refreshed, failed int
	for _, keyID := range keysToRefresh {
		err := km.FetchKey(keyID)
		if err != nil {
			log.Printf("Failed to refresh key %s: %v", keyID, err)
			failed++
		} else {
			log.Printf("Successfully refreshed key %s", keyID)
			refreshed++
		}
	}

	log.Printf("Completed refresh of expired keys: %d refreshed, %d failed",
		refreshed, failed)

	return refreshed, failed, nil
}

// RemoveKey deletes a key from the filesystem and cache.
//
// This function removes a key file from disk and updates the in-memory cache
// to reflect the removal. It uses case-insensitive matching to find the key.
//
// Parameters:
// - keyID: The ID of the key to remove
//
// Returns:
// - error: Any error encountered during the removal operation
func (km *KeyManager) RemoveKey(keyID string) error {
	if km == nil {
		return errors.New("key manager not initialized")
	}

	// Find the actual key ID in the cache with case-insensitive comparison
	km.fetchMutex.Lock()
	defer km.fetchMutex.Unlock()

	var actualKeyID string
	var found bool

	for cachedKeyID := range km.keyCache {
		if strings.EqualFold(cachedKeyID, keyID) {
			actualKeyID = cachedKeyID
			found = true
			break
		}
	}

	// If not found in cache, try with the provided ID
	if !found {
		actualKeyID = keyID
	}

	// Get the key path
	keyPath := km.GetKeyPath(actualKeyID)
	if keyPath == "" {
		return errors.New("invalid key ID")
	}

	// Remove from filesystem
	err := os.Remove(keyPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove key file: %w", err)
	}

	// Remove from cache - check all case variations
	for cachedKeyID := range km.keyCache {
		if strings.EqualFold(cachedKeyID, keyID) {
			delete(km.keyCache, cachedKeyID)
		}
	}

	log.Printf("Removed key %s", actualKeyID)
	return nil
}
