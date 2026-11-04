package basic_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/basic"
)

// Test_FileCredentialStore_VerifyCredentials_RememberedMatchAdmitsNothingElse
// verifies that a remembered password admits only itself.
//
// Once a password has matched, the store still accepts it, and still refuses
// another password of the same user, the same password presented for another
// user, and a refused password presented again.
func Test_FileCredentialStore_VerifyCredentials_RememberedMatchAdmitsNothingElse(t *testing.T) {
	t.Parallel()

	aliceHash, err := bcrypt.GenerateFromPassword([]byte("alice-secret"), bcrypt.MinCost)
	require.NoError(t, err)
	bobHash, err := bcrypt.GenerateFromPassword([]byte("bob-secret"), bcrypt.MinCost)
	require.NoError(t, err)
	carolHash, err := bcrypt.GenerateFromPassword([]byte("carol-secret"), bcrypt.MinCost)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "credentials.yaml")
	require.NoError(t, os.WriteFile(path, []byte("credentials:\n"+
		"  - username: alice\n    password_hash: "+string(aliceHash)+"\n"+
		"  - username: bob\n    password_hash: "+string(bobHash)+"\n"+
		"  - username: carol\n    password_hash: "+string(carolHash)+"\n",
	), 0o600))

	store, err := basic.NewFileCredentialStore(path)
	require.NoError(t, err)
	require.NoError(t, store.VerifyCredentials("alice", "alice-secret"))
	// carol presents only this password, so no later case can replace a
	// record that the refusal must never leave.
	require.Error(t, store.VerifyCredentials("carol", "wrong"))

	for _, tc := range []struct {
		name     string
		username string
		password string
		accepted bool
	}{
		{name: "the matched password again", username: "alice", password: "alice-secret", accepted: true},
		{name: "another password of the same user", username: "alice", password: "bob-secret"},
		{name: "the matched password for another user", username: "bob", password: "alice-secret"},
		{name: "the refused password again", username: "carol", password: "wrong"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := store.VerifyCredentials(tc.username, tc.password)
			require.Equal(t, tc.accepted, err == nil, "whether the store accepted the password, got %v", err)
		})
	}
}

// Test_FileCredentialStore_Includes verifies that composed credentials admit
// the intended identity and a missing source prevents construction.
func Test_FileCredentialStore_Includes(t *testing.T) {
	directory := t.TempDir()
	hash, err := bcrypt.GenerateFromPassword([]byte("alice-secret"), bcrypt.MinCost)
	require.NoError(t, err)
	fragment := filepath.Join(directory, "users.yaml")
	require.NoError(t, os.WriteFile(fragment, []byte("credentials:\n  - username: alice\n    password_hash: "+string(hash)+"\n"), 0o600))
	path := filepath.Join(directory, "main.yaml")
	require.NoError(t, os.WriteFile(path, []byte("include: [users.yaml]\n"), 0o600))
	store, err := basic.NewFileCredentialStore(path)
	require.NoError(t, err)
	require.NoError(t, store.VerifyCredentials("alice", "alice-secret"))
	require.Error(t, store.VerifyCredentials("alice", "wrong"))
	require.NoError(t, os.Remove(fragment))
	_, err = basic.NewFileCredentialStore(path)
	require.Error(t, err)
}
