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

// KeyManager handles GPG key operations for apt repositories
type KeyManager struct {
	config     *config.KeyManagementConfig
	keyCache   map[string]time.Time
	fetchMutex sync.RWMutex
}

// New creates a new key manager
func New(cfg *config.KeyManagementConfig, cacheDir string) (*KeyManager, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	// Get the specified key directory
	keyDir := cfg.KeyDir

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

	manager := &KeyManager{
		config:   cfg,
		keyCache: make(map[string]time.Time),
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
					manager.keyCache[keyID] = info.ModTime()
				}
			}
		}
	}

	return manager, nil
}

// DetectKeyError checks if an error response contains a missing GPG key message
// and extracts the key ID if found
func (km *KeyManager) DetectKeyError(data []byte) (string, bool) {
	if km == nil || !km.config.AutoRetrieve {
		return "", false
	}

	content := string(data)

	// Check for various key error patterns
	patterns := []string{
		`NO_PUBKEY (\w+)`,
		`KEYEXPIRED (\w+)`,
		`REVKEYSIG (\w+)`,
		`KEYREVOKED (\w+)`,
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

// FetchKey retrieves a GPG key from configured keyservers
func (km *KeyManager) FetchKey(keyID string) error {
	if km == nil || !km.config.AutoRetrieve {
		return errors.New("key management not enabled")
	}

	// Check if we already have this key and it's not expired
	km.fetchMutex.RLock()
	fetchTime, exists := km.keyCache[keyID]
	km.fetchMutex.RUnlock()

	if exists {
		keyTTL, err := time.ParseDuration(km.config.KeyTTL)
		if err != nil {
			keyTTL = 365 * 24 * time.Hour // Default to 1 year
		}

		if time.Since(fetchTime) < keyTTL {
			log.Printf("Key %s already exists and is not expired", keyID)
			return nil
		}
	}

	// Use a mutex to prevent multiple simultaneous fetches for the same key
	km.fetchMutex.Lock()
	defer km.fetchMutex.Unlock()

	// Try each keyserver until successful
	var lastErr error
	for _, server := range km.config.Keyservers {
		err := km.fetchKeyFromServer(server, keyID)
		if err == nil {
			// Update cache time
			km.keyCache[keyID] = time.Now()
			log.Printf("Successfully retrieved key %s from %s", keyID, server)
			return nil
		}
		lastErr = err
		log.Printf("Failed to retrieve key %s from %s: %v", keyID, server, err)
	}

	return fmt.Errorf("failed to retrieve key from all servers: %w", lastErr)
}

// fetchKeyFromServer retrieves a key from a specific keyserver
func (km *KeyManager) fetchKeyFromServer(server, keyID string) error {
	// For HKP keyservers
	if strings.HasPrefix(server, "hkp://") {
		server = strings.TrimPrefix(server, "hkp://")
		parts := strings.Split(server, ":")
		server = "http://" + parts[0]
		port := "11371" // Default HKP port
		if len(parts) > 1 {
			port = parts[1]
		}

		url := fmt.Sprintf("%s:%s/pks/lookup?op=get&options=mr&search=0x%s", server, port, keyID)
		return km.downloadKey(url, keyID)
	}

	return fmt.Errorf("unsupported keyserver protocol: %s", server)
}

// downloadKey fetches a key from a URL and saves it
func (km *KeyManager) downloadKey(url, keyID string) error {
	// Download the key
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("keyserver returned status: %s", resp.Status)
	}

	// Create key file
	keyPath := filepath.Join(km.config.KeyDir, keyID+".gpg")
	keyFile, err := os.Create(keyPath)
	if err != nil {
		return err
	}
	defer keyFile.Close()

	// Copy key data to file
	_, err = io.Copy(keyFile, resp.Body)
	return err
}

// GetKeyPath returns the filesystem path to a key file
func (km *KeyManager) GetKeyPath(keyID string) string {
	if km == nil {
		return ""
	}
	return filepath.Join(km.config.KeyDir, keyID+".gpg")
}

// HasKey checks if a key exists in the cache
func (km *KeyManager) HasKey(keyID string) bool {
	if km == nil {
		return false
	}

	km.fetchMutex.RLock()
	_, exists := km.keyCache[keyID]
	km.fetchMutex.RUnlock()

	return exists
}
