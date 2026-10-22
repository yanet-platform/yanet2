package xgrpc_test

import (
	"crypto/tls"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/testutils/tlscert"
	"github.com/yanet-platform/yanet2/common/go/xgrpc"
)

// pemPair is a certificate chain and its private key, PEM encoded.
type pemPair struct {
	cert []byte
	key  []byte
}

// readPEMPair returns the PEM files of an issued pair.
func readPEMPair(t *testing.T, keypair tlscert.Keypair) pemPair {
	t.Helper()

	cert, err := os.ReadFile(keypair.CertFile)
	require.NoError(t, err)
	key, err := os.ReadFile(keypair.KeyFile)
	require.NoError(t, err)

	return pemPair{cert: cert, key: key}
}

// memoryKeyPair is a key pair loader serving the pair it holds at the time
// of each load, or failing with its error while one is set.
type memoryKeyPair struct {
	pair pemPair
	err  error
}

func (m *memoryKeyPair) Source() string {
	return "memory"
}

func (m *memoryKeyPair) Load() (certPEM, keyPEM []byte, err error) {
	if m.err != nil {
		return nil, nil, m.err
	}

	return m.pair.cert, m.pair.key, nil
}

// presentedName returns the common name of the certificate a callback
// presented, empty for no certificate.
func presentedName(certificate *tls.Certificate) string {
	if certificate.Leaf == nil {
		return ""
	}

	return certificate.Leaf.Subject.CommonName
}

// Test_CertificateStore_Callbacks verifies that a store reloading on every
// use presents a renewed pair only once it loads, matches its key and is
// valid, and that a client presents the first entry the server's request
// accepts, or no certificate when none is valid and accepted.
func Test_CertificateStore_Callbacks(t *testing.T) {
	t.Parallel()

	ca := tlscert.NewCA(t)
	nextCA := ca.IssueIntermediate(t, "next CA")
	otherCA := ca.IssueIntermediate(t, "other CA")
	now := time.Now()
	current := readPEMPair(t, ca.IssueClient(t, "current"))
	renewed := readPEMPair(t, ca.IssueClient(t, "renewed"))
	staged := readPEMPair(t, ca.IssueClient(t, "staged", tlscert.WithValidity(now.Add(time.Hour), now.Add(2*time.Hour))))
	expired := readPEMPair(t, otherCA.IssueClient(t, "expired", tlscert.WithValidity(now.Add(-2*time.Hour), now.Add(-time.Hour))))
	rolled := readPEMPair(t, nextCA.IssueClient(t, "rolled"))

	type step struct {
		// load replaces the pair the first entry loads, when set.
		load *pemPair
		// loadErr fails the loads of the first entry during the step, when
		// set.
		loadErr error
		// want is the common name presented, empty for no certificate.
		want string
	}

	cases := []struct {
		name string
		// entries are the pairs the entries load at construction.
		entries []pemPair
		// accepted is the authority the server's certificate request names.
		accepted *tlscert.CA
		// server reports whether the server callback presents the pair the
		// client callback does.
		server bool
		steps  []step
	}{
		{
			name:     "renewal presents the renewed pair",
			entries:  []pemPair{current},
			accepted: ca,
			server:   true,
			steps:    []step{{load: &renewed, want: "renewed"}},
		},
		{
			name:     "rekey mid-write keeps the current pair until both files match",
			entries:  []pemPair{current},
			accepted: ca,
			server:   true,
			steps: []step{
				{load: &pemPair{cert: current.cert, key: renewed.key}, want: "current"},
				{load: &pemPair{cert: renewed.cert, key: current.key}, want: "current"},
				{load: &renewed, want: "renewed"},
			},
		},
		{
			name:     "pair not yet valid keeps the current pair",
			entries:  []pemPair{current},
			accepted: ca,
			server:   true,
			steps:    []step{{load: &staged, want: "current"}},
		},
		{
			name:     "failed load keeps the current pair",
			entries:  []pemPair{current},
			accepted: ca,
			server:   true,
			steps:    []step{{loadErr: errors.New("key pair unreadable"), want: "current"}},
		},
		{
			name:     "renewed pair already expired keeps the current pair",
			entries:  []pemPair{current},
			accepted: ca,
			server:   true,
			steps:    []step{{load: &expired, want: "current"}},
		},
		{
			name:     "CA rollover sends the second entry the server accepts",
			entries:  []pemPair{current, rolled},
			accepted: nextCA,
			steps:    []step{{want: "rolled"}},
		},
		{
			name:     "nothing acceptable sends no certificate",
			entries:  []pemPair{expired, rolled},
			accepted: otherCA,
			steps:    []step{{want: ""}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			first := &memoryKeyPair{pair: tc.entries[0]}
			loaders := []xgrpc.KeyPairLoader{first}
			for _, pair := range tc.entries[1:] {
				loaders = append(loaders, &memoryKeyPair{pair: pair})
			}
			store, err := xgrpc.NewCertificateStore(loaders, xgrpc.WithCertificateStoreRefreshInterval(0))
			require.NoError(t, err)

			hello := &tls.ClientHelloInfo{
				SupportedVersions: []uint16{tls.VersionTLS13},
				SignatureSchemes:  []tls.SignatureScheme{tls.ECDSAWithP256AndSHA256},
			}
			request := &tls.CertificateRequestInfo{
				AcceptableCAs:    [][]byte{tc.accepted.Certificate().RawSubject},
				SignatureSchemes: []tls.SignatureScheme{tls.ECDSAWithP256AndSHA256},
				Version:          tls.VersionTLS13,
			}

			for _, step := range tc.steps {
				if step.load != nil {
					first.pair = *step.load
				}
				first.err = step.loadErr

				client, err := store.GetClientCertificate(request)
				require.NoError(t, err)
				require.Equal(t, step.want, presentedName(client), "client certificate")

				if tc.server {
					server, err := store.GetCertificate(hello)
					require.NoError(t, err)
					require.Equal(t, step.want, presentedName(server), "server certificate")
				}
			}
		})
	}
}
