package x509_test

import (
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/testutils/tlscert"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/loader"
	x509auth "github.com/yanet-platform/yanet2/controlplane/internal/auth/x509"
)

// verifies reports whether keypair chains to the store's current pool as a
// client certificate.
func verifies(store *x509auth.Store, keypair tlscert.Keypair) bool {
	_, err := keypair.Leaf.Verify(x509.VerifyOptions{
		Roots:     store.Pool(),
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})

	return err == nil
}

// findMetric returns the first collected metric with the given name and
// source label.
func findMetric(t *testing.T, store *x509auth.Store, name, source string) *commonpb.Metric {
	t.Helper()

	for _, metric := range store.Collect() {
		if metric.GetName() != name {
			continue
		}
		for _, label := range metric.GetLabels() {
			if label.GetName() == "source" && label.GetValue() == source {
				return metric
			}
		}
	}

	require.Failf(t, "metric not found", "%s{source=%q}", name, source)

	return nil
}

// Test_NewStore_RejectsUntrustedRevocationList verifies that a list signed by
// an authority outside the pool fails construction instead of being ignored.
func Test_NewStore_RejectsUntrustedRevocationList(t *testing.T) {
	ca := tlscert.NewCA(t)
	other := tlscert.NewCA(t)
	crlFile := other.RevocationListFile(t, time.Now().Add(time.Hour))

	_, err := x509auth.NewStore(
		[]loader.Loader{loader.NewLoader(ca.BundleFile())},
		[]loader.Loader{loader.NewLoader(crlFile)},
	)
	require.ErrorIs(t, err, x509auth.ErrUntrustedRevocationList)
}

// Test_NewStore_RejectsBundleWithoutCertificates verifies that a bundle
// holding no certificate fails construction.
func Test_NewStore_RejectsBundleWithoutCertificates(t *testing.T) {
	bundle := filepath.Join(t.TempDir(), "empty.pem")
	require.NoError(t, os.WriteFile(bundle, []byte("# nothing here\n"), 0o600))

	_, err := x509auth.NewStore([]loader.Loader{loader.NewLoader(bundle)}, nil)
	require.ErrorIs(t, err, x509auth.ErrNoAuthorities)
}

// Test_NewStore_AcceptsPEMRevocationList verifies that a PEM-armored list is
// read the same as a DER one.
func Test_NewStore_AcceptsPEMRevocationList(t *testing.T) {
	ca := tlscert.NewCA(t)
	revoked := ca.IssueClient(t, "route-operator")

	crlFile := filepath.Join(t.TempDir(), "revoked.pem")
	armored := pem.EncodeToMemory(&pem.Block{
		Type:  "X509 CRL",
		Bytes: ca.RevocationList(t, time.Now().Add(time.Hour), revoked.Leaf),
	})
	require.NoError(t, os.WriteFile(crlFile, armored, 0o600))

	store := newStore(t, ca, crlFile)
	require.True(t, store.IsRevoked(revoked.Leaf))
}

// Test_Store_Reload_KeepsSnapshotWhenAuthoritiesFail verifies that a bundle
// that no longer loads leaves the previous authorities and revocations in
// force and is counted against its source.
func Test_Store_Reload_KeepsSnapshotWhenAuthoritiesFail(t *testing.T) {
	ca := tlscert.NewCA(t)
	client := ca.IssueClient(t, "route-operator")
	revoked := ca.IssueClient(t, "pipeline-operator")
	crlFile := ca.RevocationListFile(t, time.Now().Add(time.Hour), revoked.Leaf)
	store := newStore(t, ca, crlFile)

	require.NoError(t, os.WriteFile(ca.BundleFile(), []byte("garbage"), 0o600))

	require.Error(t, store.Reload())
	require.True(t, verifies(store, client))
	require.True(t, store.IsRevoked(revoked.Leaf))
	require.EqualValues(t, 1, findMetric(t, store, "auth_x509_refresh_errors_total", ca.BundleFile()).GetCounter())
	require.EqualValues(t, 0, findMetric(t, store, "auth_x509_refresh_errors_total", crlFile).GetCounter())
}

// Test_Store_Reload_KeepsRevocationListWhenSourceFails verifies that a list
// that no longer loads or verifies keeps its last accepted content while the
// authorities still refresh.
func Test_Store_Reload_KeepsRevocationListWhenSourceFails(t *testing.T) {
	ca := tlscert.NewCA(t)
	other := tlscert.NewCA(t)
	revoked := ca.IssueClient(t, "route-operator")
	crlFile := ca.RevocationListFile(t, time.Now().Add(time.Hour), revoked.Leaf)
	store := newStore(t, ca, crlFile)

	tests := []struct {
		name    string
		content []byte
	}{
		{name: "unparseable", content: []byte("garbage")},
		{name: "signed by an untrusted authority", content: other.RevocationList(t, time.Now().Add(time.Hour))},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			require.NoError(t, os.WriteFile(crlFile, testCase.content, 0o600))

			require.Error(t, store.Reload())
			require.True(t, store.IsRevoked(revoked.Leaf))
			require.True(t, verifies(store, revoked))
		})
	}

	require.EqualValues(t, 2, findMetric(t, store, "auth_x509_refresh_errors_total", crlFile).GetCounter())
}

// Test_Store_Reload_PicksUpNewRevocations verifies that a reload of an
// updated list revokes a certificate the previous snapshot accepted.
func Test_Store_Reload_PicksUpNewRevocations(t *testing.T) {
	ca := tlscert.NewCA(t)
	client := ca.IssueClient(t, "route-operator")
	crlFile := ca.RevocationListFile(t, time.Now().Add(time.Hour))
	store := newStore(t, ca, crlFile)
	require.False(t, store.IsRevoked(client.Leaf))

	require.NoError(t, os.WriteFile(crlFile, ca.RevocationList(t, time.Now().Add(time.Hour), client.Leaf), 0o600))

	require.NoError(t, store.Reload())
	require.True(t, store.IsRevoked(client.Leaf))
}

// Test_Store_Collect_ReportsNextUpdate verifies that every loaded list
// reports the time its issuer promised the next one by, so a stale list is
// visible without reading logs.
func Test_Store_Collect_ReportsNextUpdate(t *testing.T) {
	ca := tlscert.NewCA(t)
	nextUpdate := time.Now().Add(-time.Hour).Truncate(time.Second)
	crlFile := ca.RevocationListFile(t, nextUpdate)
	store := newStore(t, ca, crlFile)

	require.Equal(t, []x509auth.RevocationListInfo{{Source: crlFile, NextUpdate: nextUpdate.UTC()}}, store.RevocationLists())
	require.EqualValues(t, nextUpdate.Unix(), findMetric(t, store, "auth_x509_crl_next_update_seconds", crlFile).GetGauge())
}

// Test_NewStore_RejectsEndEntityInBundle verifies that a bundle holding a
// certificate that is not an authority fails construction, since such a
// certificate in the pool would verify itself.
func Test_NewStore_RejectsEndEntityInBundle(t *testing.T) {
	ca := tlscert.NewCA(t)
	client := ca.IssueClient(t, "route-operator")

	_, err := x509auth.NewStore([]loader.Loader{loader.NewLoader(client.CertFile)}, nil)
	require.ErrorIs(t, err, x509auth.ErrNotAuthority)
}

// Test_Store_Reload_RejectsOlderRevocationList verifies that a correctly
// signed but older list does not replace the accepted one, so a replayed
// list cannot undo a revocation.
func Test_Store_Reload_RejectsOlderRevocationList(t *testing.T) {
	ca := tlscert.NewCA(t)
	client := ca.IssueClient(t, "route-operator")
	older := ca.RevocationList(t, time.Now().Add(time.Hour))
	crlFile := ca.RevocationListFile(t, time.Now().Add(time.Hour), client.Leaf)
	store := newStore(t, ca, crlFile)
	require.True(t, store.IsRevoked(client.Leaf))

	require.NoError(t, os.WriteFile(crlFile, older, 0o600))

	require.ErrorIs(t, store.Reload(), x509auth.ErrRevocationListRollback)
	require.True(t, store.IsRevoked(client.Leaf))
	require.EqualValues(t, 1, findMetric(t, store, "auth_x509_refresh_errors_total", crlFile).GetCounter())
}

// Test_Store_Collect_RedactsSourceURL verifies that a source fetched from a
// URL is labeled without the credentials and query the URL carries.
func Test_Store_Collect_RedactsSourceURL(t *testing.T) {
	ca := tlscert.NewCA(t)
	bundle, err := os.ReadFile(ca.BundleFile())
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bundle)
	}))
	t.Cleanup(server.Close)

	source := strings.Replace(server.URL, "http://", "http://user:secret@", 1) + "/ca.pem?token=secret"
	store, err := x509auth.NewStore([]loader.Loader{loader.NewLoader(source)}, nil)
	require.NoError(t, err)

	require.EqualValues(t, 0, findMetric(t, store, "auth_x509_refresh_errors_total", server.URL+"/ca.pem").GetCounter())
	for _, metric := range store.Collect() {
		for _, label := range metric.GetLabels() {
			require.NotContains(t, label.GetValue(), "secret")
		}
	}
}
