package x509

import (
	"context"
	"crypto/x509"
	"slices"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
)

const (
	// defaultRefreshInterval is the default period between reloads of the
	// authorities and revocation lists.
	defaultRefreshInterval = 5 * time.Minute
	// spiffeScheme is the URI scheme of a SPIFFE identity.
	spiffeScheme = "spiffe"
)

// Authenticator identifies a caller by the client certificate its TLS
// connection presented.
//
// The handshake already verified the chain against the store's authorities,
// so a request only has to pass what changes over time: the validity period
// and revocation. The certificate's common name, or its single SPIFFE
// identity when the name is empty, becomes the local account lookup name.
type Authenticator struct {
	store           *Store
	refreshInterval time.Duration
	stopCh          chan struct{}
	log             *zap.Logger
}

type authenticatorOptions struct {
	RefreshInterval time.Duration
	Log             *zap.Logger
}

func newAuthenticatorOptions() *authenticatorOptions {
	return &authenticatorOptions{
		RefreshInterval: defaultRefreshInterval,
		Log:             zap.NewNop(),
	}
}

// Option configures the Authenticator.
type Option func(*authenticatorOptions)

// WithRefreshInterval sets the periodic refresh interval.
func WithRefreshInterval(d time.Duration) Option {
	return func(o *authenticatorOptions) {
		o.RefreshInterval = d
	}
}

// WithLog sets the logger.
func WithLog(log *zap.Logger) Option {
	return func(o *authenticatorOptions) {
		o.Log = log
	}
}

// NewAuthenticator creates an Authenticator over store and starts its
// periodic refresh.
func NewAuthenticator(store *Store, options ...Option) *Authenticator {
	opts := newAuthenticatorOptions()
	for _, o := range options {
		o(opts)
	}

	m := &Authenticator{
		store:           store,
		refreshInterval: opts.RefreshInterval,
		stopCh:          make(chan struct{}),
		log:             opts.Log,
	}

	go m.refreshLoop()

	return m
}

// Name returns the authenticator name for logging.
func (m *Authenticator) Name() string {
	return "x509"
}

// Supports reports whether the credential is a verified client certificate
// and nothing more explicit.
//
// A token is the caller's explicit choice of identity and always wins over
// the certificate its connection happens to carry.
func (m *Authenticator) Supports(credential core.Credential) bool {
	return credential.Token == "" && credential.TLS != nil && len(credential.TLS.VerifiedChains) > 0
}

// Authenticate checks that the verified leaf certificate is currently valid
// and not revoked, and derives the login from it.
func (m *Authenticator) Authenticate(
	ctx context.Context,
	credential core.Credential,
	reqInfo *core.RequestInfo,
) (*core.AuthInfo, error) {
	chain, ok := verifiedChain(credential)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, ErrNoVerifiedCertificate.Error())
	}
	leaf := chain[0]

	// The handshake's verdict ages with the connection and never consulted
	// the revocation lists, so every link of the chain is checked now.
	now := time.Now()
	for _, certificate := range chain {
		if err := checkValidity(certificate, now); err != nil {
			return nil, status.Errorf(
				codes.Unauthenticated,
				"certificate validity check failed: %v", err,
			)
		}
	}

	if slices.ContainsFunc(chain, m.store.IsRevoked) {
		return nil, status.Error(codes.Unauthenticated, ErrCertificateRevoked.Error())
	}

	login, err := subjectLogin(leaf)
	if err != nil {
		return nil, status.Errorf(
			codes.Unauthenticated,
			"failed to extract principal: %v", err,
		)
	}

	m.log.Debug("x509 authentication successful",
		zap.String("login", login),
		zap.Stringer("serial", leaf.SerialNumber),
	)

	subject := core.NewLocalSubject(login)
	subject.Assertion = CertificateAssertion{Leaf: leaf}

	return &core.AuthInfo{
		Subject:    subject,
		AuthMethod: "x509",
	}, nil
}

// ClientCAs returns the authorities a TLS server verifies client
// certificates against, current as of the last successful reload.
func (m *Authenticator) ClientCAs() *x509.CertPool {
	return m.store.Pool()
}

// Collect reports the revocation list and refresh metrics of the store.
func (m *Authenticator) Collect() []*commonpb.Metric {
	return m.store.Collect()
}

// Close stops the periodic refresh.
func (m *Authenticator) Close() {
	close(m.stopCh)
}

// refreshLoop reloads the trust material periodically until Close.
func (m *Authenticator) refreshLoop() {
	ticker := time.NewTicker(m.refreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.refresh()
		}
	}
}

// refresh reloads the trust material and reports lists past their next
// update, which stay in use so a stalled PKI does not lock callers out.
func (m *Authenticator) refresh() {
	if err := m.store.Reload(); err != nil {
		m.log.Warn("failed to refresh x509 trust material", zap.Error(err))
	}

	now := time.Now()
	for _, info := range m.store.RevocationLists() {
		if now.After(info.NextUpdate) {
			m.log.Warn("revocation list is past its next update",
				zap.String("source", info.Source),
				zap.Time("next_update", info.NextUpdate),
			)
		}
	}
}

// verifiedChain returns the chain the handshake verified, the client
// certificate first.
func verifiedChain(credential core.Credential) ([]*x509.Certificate, bool) {
	if credential.TLS == nil || len(credential.TLS.VerifiedChains) == 0 || len(credential.TLS.VerifiedChains[0]) == 0 {
		return nil, false
	}

	return credential.TLS.VerifiedChains[0], true
}

// checkValidity checks that now falls within the certificate's validity
// period.
func checkValidity(certificate *x509.Certificate, now time.Time) error {
	if now.Before(certificate.NotBefore) {
		return ErrCertificateNotYetValid
	}
	if now.After(certificate.NotAfter) {
		return ErrCertificateExpired
	}

	return nil
}

// subjectLogin derives the account lookup name: the common name, or the
// single SPIFFE identity of a certificate without one.
func subjectLogin(certificate *x509.Certificate) (string, error) {
	if name := certificate.Subject.CommonName; name != "" {
		return name, nil
	}

	var identities []string
	for _, uri := range certificate.URIs {
		if uri.Scheme == spiffeScheme {
			identities = append(identities, uri.String())
		}
	}
	if len(identities) == 1 {
		return identities[0], nil
	}

	return "", ErrNoSubjectName
}
