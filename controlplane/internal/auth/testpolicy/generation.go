// Package testpolicy builds deterministic managed policy fixtures for tests.
package testpolicy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth"
)

// Scope is a node-specific authority independent of the published data.
func Scope() auth.PolicyScope {
	return auth.PolicyScope{
		Epoch: "installation-uid", NodeUID: "node-uid",
		ServerURI: "spiffe://example.test/ns/default/installation/installation-uid/node/node-uid/role/gateway",
	}
}

// Generation specifies bytes to commit under current.
type Generation struct {
	Scope               auth.PolicyScope
	Sequence            uint64
	Identities          string
	Permissions         string
	SSHTrust            []byte
	OmitFile            string
	ChangeFileBytes     string
	WrongGenerationName bool
}

// ForAccount builds a single account and its explicit set of methods.
func ForAccount(sequence uint64, username string, methods ...string) Generation {
	return Generation{
		Scope: Scope(), Sequence: sequence,
		Identities: fmt.Sprintf("identities: [{username: %q}]\n", username),
		Permissions: fmt.Sprintf("permissions: {users: [{username: %q, permissions: [%s]}]}\n",
			username, strings.Join(methods, ", "),
		),
	}
}

// Config selects a fixture's scope and polls without the normal production delay.
func Config(directory string) *auth.ManagedPolicyConfig {
	return &auth.ManagedPolicyConfig{
		Directory: directory, ExpectedScope: Scope(), RefreshInterval: 5 * time.Millisecond,
	}
}

// Start loads the initial generation and stops the updater with the test context.
func Start(t *testing.T, config *auth.Config) *auth.Manager {
	t.Helper()
	manager, err := auth.NewManager(config)
	require.NoError(t, err)
	t.Cleanup(manager.Close)
	var group errgroup.Group
	group.Go(func() error { return manager.RunPolicyReload(t.Context()) })
	t.Cleanup(func() { require.NoError(t, group.Wait()) })
	return manager
}

// Metric finds a metric by its exported name, or returns nil if it is absent.
func Metric(metrics []*commonpb.Metric, name string) *commonpb.Metric {
	for _, metric := range metrics {
		if metric.GetName() == name {
			return metric
		}
	}
	return nil
}

// Publish writes and switches one test fixture without using the reader.
func (m Generation) Publish(t *testing.T, directory string) {
	t.Helper()

	digest := func(data []byte) string {
		sum := sha256.Sum256(data)
		return hex.EncodeToString(sum[:])
	}
	manifest := struct {
		Version    int              `json:"version"`
		Scope      auth.PolicyScope `json:"scope"`
		Sequence   uint64           `json:"sequence"`
		Identities string           `json:"identities_sha256"`
		Grants     string           `json:"permissions_sha256"`
		SSHTrust   string           `json:"ssh_sha256,omitempty"`
	}{1, m.Scope, m.Sequence, digest([]byte(m.Identities)), digest([]byte(m.Permissions)), ""}
	if m.SSHTrust != nil {
		manifest.SSHTrust = digest(m.SSHTrust)
	}
	data, err := json.Marshal(manifest)
	require.NoError(t, err)
	generation := digest(data)
	if m.WrongGenerationName {
		generation = digest([]byte("different generation"))
	}
	path := filepath.Join(directory, generation)
	require.NoError(t, os.MkdirAll(path, 0o700))
	lock, err := os.OpenFile(filepath.Join(directory, ".publish-lock"), os.O_CREATE|os.O_RDONLY, 0o600)
	require.NoError(t, err)
	require.NoError(t, lock.Close())
	files := map[string][]byte{
		"policy.json": data, "identities.yaml": []byte(m.Identities),
		"permissions.yaml": []byte(m.Permissions), "ssh-ca.pub": m.SSHTrust,
	}
	for name, content := range files {
		if content == nil || name == m.OmitFile {
			continue
		}
		if name == m.ChangeFileBytes {
			content = append(content, '\n')
		}
		require.NoError(t, os.WriteFile(filepath.Join(path, name), content, 0o600))
	}
	next := filepath.Join(directory, ".current-next")
	require.NoError(t, os.Symlink(generation, next))
	require.NoError(t, os.Rename(next, filepath.Join(directory, "current")))
}
