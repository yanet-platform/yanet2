package sshcert_test

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
	"golang.org/x/sync/errgroup"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/sshcert"
)

type testLoader struct {
	data []byte
}

func (m testLoader) Source() string {
	return "test"
}

func (m testLoader) Load() ([]byte, error) {
	return m.data, nil
}

type mutableLoader struct {
	mu       sync.RWMutex
	data     []byte
	err      error
	nextLoad *blockedLoad
}

type blockedLoad struct {
	data    []byte
	started chan struct{}
	release chan struct{}
}

func newMutableLoader(data []byte) *mutableLoader {
	return &mutableLoader{data: append([]byte(nil), data...)}
}

func (m *mutableLoader) Source() string {
	return "mutable"
}

func (m *mutableLoader) Load() ([]byte, error) {
	m.mu.Lock()
	if m.nextLoad != nil {
		load := m.nextLoad
		m.nextLoad = nil
		data := append([]byte(nil), load.data...)
		close(load.started)
		m.mu.Unlock()

		<-load.release
		return data, nil
	}

	data := append([]byte(nil), m.data...)
	err := m.err
	m.mu.Unlock()

	return data, err
}

func (m *mutableLoader) Set(data []byte, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.data = append([]byte(nil), data...)
	m.err = err
}

func (m *mutableLoader) ArmNextLoad(data []byte) *blockedLoad {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.nextLoad != nil {
		panic("mutable loader already has a blocked load")
	}

	load := &blockedLoad{
		data:    append([]byte(nil), data...),
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	m.nextLoad = load

	return load
}

func authorizedKeysData(signers ...ssh.Signer) []byte {
	var data []byte
	for _, signer := range signers {
		data = append(data, ssh.MarshalAuthorizedKey(signer.PublicKey())...)
	}

	return data
}

func TestCAStore_VerifyCA_Trusted(t *testing.T) {
	ca := generateTestCA(t)
	cert, _ := generateTestUserCert(
		t, ca, "alice", 1,
		time.Now().Add(-1*time.Hour),
		time.Now().Add(24*time.Hour),
	)

	store := sshcert.NewCAStore([]sshcert.CAEntry{
		{PublicKey: ca.PublicKey()},
	})

	err := store.VerifyCA(cert)
	require.NoError(t, err)
}

func TestCAStore_VerifyCA_Untrusted(t *testing.T) {
	ca1 := generateTestCA(t)
	ca2 := generateTestCA(t)

	// Certificate signed by ca1.
	cert, _ := generateTestUserCert(
		t, ca1, "alice", 1,
		time.Now().Add(-1*time.Hour),
		time.Now().Add(24*time.Hour),
	)

	// Store only has ca2.
	store := sshcert.NewCAStore([]sshcert.CAEntry{
		{PublicKey: ca2.PublicKey()},
	})

	err := store.VerifyCA(cert)
	require.ErrorIs(t, err, sshcert.ErrUntrustedCA)
}

func TestCAStore_VerifyCA_MultipleCAs(t *testing.T) {
	ca1 := generateTestCA(t)
	ca2 := generateTestCA(t)

	// Certificate signed by ca2.
	cert, _ := generateTestUserCert(
		t, ca2, "alice", 1,
		time.Now().Add(-1*time.Hour),
		time.Now().Add(24*time.Hour),
	)

	store := sshcert.NewCAStore([]sshcert.CAEntry{
		{PublicKey: ca1.PublicKey()},
		{PublicKey: ca2.PublicKey()},
	})

	err := store.VerifyCA(cert)
	require.NoError(t, err)
}

func TestCAStore_VerifyCA_EmptyStore(t *testing.T) {
	ca := generateTestCA(t)
	cert, _ := generateTestUserCert(
		t, ca, "alice", 1,
		time.Now().Add(-1*time.Hour),
		time.Now().Add(24*time.Hour),
	)

	store := sshcert.NewCAStore(nil)

	err := store.VerifyCA(cert)
	require.ErrorIs(t, err, sshcert.ErrUntrustedCA)
}

func TestCAStore_LoadAuthorizedKeys(t *testing.T) {
	ca1 := generateTestCA(t)
	ca2 := generateTestCA(t)

	// Build authorized_keys format like cauth returns.
	key1 := strings.TrimSpace(
		string(ssh.MarshalAuthorizedKey(ca1.PublicKey())),
	)
	key2 := strings.TrimSpace(
		string(ssh.MarshalAuthorizedKey(ca2.PublicKey())),
	)
	data := key1 + "\n" + key2 + " secure_20211011\n"

	store, err := sshcert.NewCAStoreFromLoader(testLoader{data: []byte(data)})
	require.NoError(t, err)
	certOne, _ := generateTestUserCert(
		t, ca1, "alice", 1,
		time.Now().Add(-time.Hour), time.Now().Add(time.Hour),
	)
	certTwo, _ := generateTestUserCert(
		t, ca2, "alice", 1,
		time.Now().Add(-time.Hour), time.Now().Add(time.Hour),
	)
	require.NoError(t, store.VerifyCA(certOne))
	require.NoError(t, store.VerifyCA(certTwo))
}

func TestCAStore_LoadAuthorizedKeys_WithComments(t *testing.T) {
	ca := generateTestCA(t)

	key := strings.TrimSpace(
		string(ssh.MarshalAuthorizedKey(ca.PublicKey())),
	)
	data := "# This is a comment\n\n" + key + " my-ca\n"

	store, err := sshcert.NewCAStoreFromLoader(testLoader{data: []byte(data)})
	require.NoError(t, err)
	cert, _ := generateTestUserCert(
		t, ca, "alice", 1,
		time.Now().Add(-time.Hour), time.Now().Add(time.Hour),
	)
	require.NoError(t, store.VerifyCA(cert))
}

func TestParseCAData_VerifyCA(t *testing.T) {
	ca := generateTestCA(t)
	cert, _ := generateTestUserCert(
		t, ca, "alice", 1,
		time.Now().Add(-1*time.Hour),
		time.Now().Add(24*time.Hour),
	)

	key := strings.TrimSpace(
		string(ssh.MarshalAuthorizedKey(ca.PublicKey())),
	)
	data := key + " current\n"

	store, err := sshcert.NewCAStoreFromLoader(testLoader{data: []byte(data)})
	require.NoError(t, err)
	err = store.VerifyCA(cert)
	require.NoError(t, err)
}

func TestCAStore_Reload(t *testing.T) {
	caOne := generateTestCA(t)
	caTwo := generateTestCA(t)
	loader := newMutableLoader(authorizedKeysData(caOne))

	store, err := sshcert.NewCAStoreFromLoader(loader)
	require.NoError(t, err)

	certOne, _ := generateTestUserCert(
		t, caOne, "alice", 1,
		time.Now().Add(-time.Hour), time.Now().Add(time.Hour),
	)
	certTwo, _ := generateTestUserCert(
		t, caTwo, "alice", 2,
		time.Now().Add(-time.Hour), time.Now().Add(time.Hour),
	)

	require.NoError(t, store.VerifyCA(certOne))

	loader.Set(authorizedKeysData(caTwo), nil)
	require.NoError(t, store.Reload())
	require.NoError(t, store.VerifyCA(certTwo))
	require.ErrorIs(t, store.VerifyCA(certOne), sshcert.ErrUntrustedCA)

	loader.Set(nil, errors.New("loader unavailable"))
	err = store.Reload()
	require.ErrorContains(t, err, "failed to reload CA data")
	require.NoError(t, store.VerifyCA(certTwo))

	loader.Set([]byte("not an authorized key"), nil)
	err = store.Reload()
	require.ErrorContains(t, err, "failed to parse CA data")
	require.NoError(t, store.VerifyCA(certTwo))
}

func TestCAStore_ReloadConcurrentVerify(t *testing.T) {
	sharedCA := generateTestCA(t)
	rotatingCAOne := generateTestCA(t)
	rotatingCATwo := generateTestCA(t)
	loader := newMutableLoader(authorizedKeysData(sharedCA, rotatingCAOne))
	store, err := sshcert.NewCAStoreFromLoader(loader)
	require.NoError(t, err)

	sharedCert, _ := generateTestUserCert(
		t, sharedCA, "alice", 1,
		time.Now().Add(-time.Hour), time.Now().Add(time.Hour),
	)
	rotatingCertOne, _ := generateTestUserCert(
		t, rotatingCAOne, "alice", 2,
		time.Now().Add(-time.Hour), time.Now().Add(time.Hour),
	)
	rotatingCertTwo, _ := generateTestUserCert(
		t, rotatingCATwo, "alice", 3,
		time.Now().Add(-time.Hour), time.Now().Add(time.Hour),
	)
	snapshots := [][]byte{
		authorizedKeysData(sharedCA, rotatingCAOne),
		authorizedKeysData(sharedCA, rotatingCATwo),
	}
	rotatingCerts := []*ssh.Certificate{rotatingCertOne, rotatingCertTwo}

	const reloadIterations = 100
	for idx := range reloadIterations {
		snapshotIndex := idx % len(snapshots)
		load := loader.ArmNextLoad(snapshots[snapshotIndex])

		var group errgroup.Group
		group.Go(func() error {
			return store.Reload()
		})

		<-load.started
		verifyErr := store.VerifyCA(sharedCert)
		close(load.release)
		require.NoError(t, verifyErr)
		require.NoError(t, group.Wait())
		require.NoError(t, store.VerifyCA(rotatingCerts[snapshotIndex]))
	}
}
