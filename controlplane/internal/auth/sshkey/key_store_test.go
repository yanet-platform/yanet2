package sshkey_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/sshkey"
)

func TestKeyStore_GetKeys(t *testing.T) {
	signer := generateTestEd25519Signer(t)

	store := sshkey.NewKeyStore(map[string][]sshkey.KeyEntry{
		"alice": {
			{
				PublicKey: signer.PublicKey(),
				Comment:   "alice key",
			},
		},
	})

	keys := store.GetKeys("alice")
	require.Len(t, keys, 1)
}

func TestKeyStore_DisabledKeyFiltered(t *testing.T) {
	signer := generateTestEd25519Signer(t)

	store := sshkey.NewKeyStore(map[string][]sshkey.KeyEntry{
		"alice": {
			{
				PublicKey: signer.PublicKey(),
				Comment:   "disabled",
				Disabled:  true,
			},
		},
	})

	keys := store.GetKeys("alice")
	assert.Empty(t, keys)
}

func TestKeyStore_UnknownUser(t *testing.T) {
	store := sshkey.NewKeyStore(map[string][]sshkey.KeyEntry{})

	keys := store.GetKeys("unknown")
	assert.Nil(t, keys)
}

func TestKeyStore_MultipleKeysPerUser(t *testing.T) {
	signer1 := generateTestEd25519Signer(t)
	signer2 := generateTestRSASigner(t)

	store := sshkey.NewKeyStore(map[string][]sshkey.KeyEntry{
		"alice": {
			{
				PublicKey: signer1.PublicKey(),
				Comment:   "key 1",
			},
			{
				PublicKey: signer2.PublicKey(),
				Comment:   "key 2",
			},
		},
	})

	keys := store.GetKeys("alice")
	assert.Len(t, keys, 2)
}

func TestKeyStoreFromFile_InvalidFile(t *testing.T) {
	_, err := sshkey.NewKeyStoreFromFile("/nonexistent/path.yaml")
	require.Error(t, err)
}
