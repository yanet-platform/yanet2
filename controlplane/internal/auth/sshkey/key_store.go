package sshkey

import (
	"fmt"
	"os"

	"golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/common/go/rcucache"
)

// KeyEntry represents a single SSH public key entry.
type KeyEntry struct {
	// PublicKey is the parsed SSH public key.
	PublicKey ssh.PublicKey
	// Comment is an optional human-readable description.
	Comment string
	// Disabled indicates whether this key is disabled.
	Disabled bool
}

type userName = string

// KeyStore stores SSH public keys indexed by username.
//
// One user can have multiple keys. Disabled keys are filtered out at
// lookup time.
type KeyStore struct {
	// keys maps username -> list of key entries.
	keys *rcucache.Cache[userName, []KeyEntry]
}

// NewKeyStore creates a KeyStore from an in-memory map.
func NewKeyStore(keys map[string][]KeyEntry) *KeyStore {
	return &KeyStore{
		keys: rcucache.NewCache(keys),
	}
}

// NewKeyStoreFromFile creates a KeyStore by loading keys from a YAML
// file.
//
// The file format:
//
//	keys:
//	  - username: alice
//	    public_key: "ssh-ed25519 AAAAC3..."
//	    comment: "Alice's workstation key"
//	    disabled: false
func NewKeyStoreFromFile(path string) (*KeyStore, error) {
	keys, err := loadKeysFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to load SSH keys: %w", err)
	}

	return NewKeyStore(keys), nil
}

// GetKeys returns all non-disabled SSH public keys for the given
// username.
func (m *KeyStore) GetKeys(username string) []ssh.PublicKey {
	view := m.keys.View()

	entries, ok := view.Lookup(username)
	if !ok {
		return nil
	}

	var keys []ssh.PublicKey
	for _, entry := range entries {
		if !entry.Disabled {
			keys = append(keys, entry.PublicKey)
		}
	}

	return keys
}

// loadKeysFile reads and parses the SSH keys YAML file into a map.
func loadKeysFile(path string) (map[string][]KeyEntry, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	var file struct {
		Keys []struct {
			Username  string `yaml:"username"`
			PublicKey string `yaml:"public_key"`
			Comment   string `yaml:"comment"`
			Disabled  bool   `yaml:"disabled"`
		} `yaml:"keys"`
	}

	if err := yaml.Unmarshal(buf, &file); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	// Build username -> []KeyEntry index.
	keysMap := map[string][]KeyEntry{}
	for _, entry := range file.Keys {
		if entry.Username == "" {
			return nil, fmt.Errorf("key entry with empty username")
		}

		if entry.PublicKey == "" {
			return nil, fmt.Errorf(
				"key entry for user %q with empty public_key",
				entry.Username,
			)
		}

		pubKey, _, _, _, err := ssh.ParseAuthorizedKey(
			[]byte(entry.PublicKey),
		)
		if err != nil {
			return nil, fmt.Errorf(
				"failed to parse public key for user %q: %w",
				entry.Username, err,
			)
		}

		keysMap[entry.Username] = append(keysMap[entry.Username], KeyEntry{
			PublicKey: pubKey,
			Comment:   entry.Comment,
			Disabled:  entry.Disabled,
		})
	}

	return keysMap, nil
}
