package sshcert

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

func TestCAStore_VerifyCA_Trusted(t *testing.T) {
	ca := generateCA(t)
	cert, _ := generateUserCert(
		t, ca, "alice", 1,
		time.Now().Add(-1*time.Hour),
		time.Now().Add(24*time.Hour),
	)

	store := NewCAStore([]CAEntry{
		{PublicKey: ca.PublicKey()},
	})

	err := store.VerifyCA(cert)
	require.NoError(t, err)
}

func TestCAStore_VerifyCA_Untrusted(t *testing.T) {
	ca1 := generateCA(t)
	ca2 := generateCA(t)

	// Certificate signed by ca1.
	cert, _ := generateUserCert(
		t, ca1, "alice", 1,
		time.Now().Add(-1*time.Hour),
		time.Now().Add(24*time.Hour),
	)

	// Store only has ca2.
	store := NewCAStore([]CAEntry{
		{PublicKey: ca2.PublicKey()},
	})

	err := store.VerifyCA(cert)
	require.ErrorIs(t, err, ErrUntrustedCA)
}

func TestCAStore_VerifyCA_MultipleCAs(t *testing.T) {
	ca1 := generateCA(t)
	ca2 := generateCA(t)

	// Certificate signed by ca2.
	cert, _ := generateUserCert(
		t, ca2, "alice", 1,
		time.Now().Add(-1*time.Hour),
		time.Now().Add(24*time.Hour),
	)

	store := NewCAStore([]CAEntry{
		{PublicKey: ca1.PublicKey()},
		{PublicKey: ca2.PublicKey()},
	})

	err := store.VerifyCA(cert)
	require.NoError(t, err)
}

func TestCAStore_VerifyCA_EmptyStore(t *testing.T) {
	ca := generateCA(t)
	cert, _ := generateUserCert(
		t, ca, "alice", 1,
		time.Now().Add(-1*time.Hour),
		time.Now().Add(24*time.Hour),
	)

	store := NewCAStore(nil)

	err := store.VerifyCA(cert)
	require.ErrorIs(t, err, ErrUntrustedCA)
}

func TestParseCAData(t *testing.T) {
	ca1 := generateCA(t)
	ca2 := generateCA(t)

	// Build authorized_keys format like cauth returns.
	key1 := strings.TrimSpace(
		string(ssh.MarshalAuthorizedKey(ca1.PublicKey())),
	)
	key2 := strings.TrimSpace(
		string(ssh.MarshalAuthorizedKey(ca2.PublicKey())),
	)
	data := key1 + "\n" + key2 + " secure_20211011\n"

	entries, err := parseCAData([]byte(data))
	require.NoError(t, err)
	require.Len(t, entries, 2)
}

func TestParseCAData_WithComments(t *testing.T) {
	ca := generateCA(t)

	key := strings.TrimSpace(
		string(ssh.MarshalAuthorizedKey(ca.PublicKey())),
	)
	data := "# This is a comment\n\n" + key + " my-ca\n"

	entries, err := parseCAData([]byte(data))
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

func TestParseCAData_VerifyCA(t *testing.T) {
	ca := generateCA(t)
	cert, _ := generateUserCert(
		t, ca, "alice", 1,
		time.Now().Add(-1*time.Hour),
		time.Now().Add(24*time.Hour),
	)

	key := strings.TrimSpace(
		string(ssh.MarshalAuthorizedKey(ca.PublicKey())),
	)
	data := key + " current\n"

	entries, err := parseCAData([]byte(data))
	require.NoError(t, err)

	store := NewCAStore(entries)
	err = store.VerifyCA(cert)
	require.NoError(t, err)
}
