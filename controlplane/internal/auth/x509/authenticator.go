package x509

import (
	"context"
	"crypto/x509"
	"maps"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/metrics"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
)

const (
	// defaultRefreshInterval is the default period between reloads of the
	// authorities and revocation lists.
	defaultRefreshInterval = 5 * time.Minute
	// spiffeScheme is the URI scheme of a SPIFFE identity.
	spiffeScheme = "spiffe"
	// metricClientCertificateNotAfter is the unix time the earliest expiring
	// certificate a user recently presented expires at.
	metricClientCertificateNotAfter = "auth_x509_client_certificate_not_after_seconds"
	// labelUser names the account a metric is about.
	labelUser = "user"
	// labelNotAfter keys a presented certificate by its expiry beside its
	// user; no reported series carries it.
	labelNotAfter = "not_after"
	// presentedRetention is how long a presented certificate counts toward
	// its user's expiry after its last use.
	presentedRetention = time.Hour
)

// presentedCertificate is a certificate expiry one user presented, with
// when that last happened.
type presentedCertificate struct {
	// NotAfter is when the presented certificate expires.
	NotAfter time.Time
	// LastSeen is the unix time in nanoseconds of the latest call that
	// presented it.
	LastSeen atomic.Int64
}

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
	// presentedMu makes recording a presented certificate and evicting
	// stale ones exclusive, so a recording never stamps an entry that an
	// eviction has already removed.
	presentedMu sync.RWMutex
	presented   *metrics.MetricMap[*presentedCertificate]
	stopCh      chan struct{}
	log         *zap.Logger
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
		presented:       metrics.NewMetricMap[*presentedCertificate](),
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
	m.recordPresented(login, leaf, now)

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

// Collect reports the revocation list and refresh metrics of the store and
// the expiry of the certificates each user recently presented.
func (m *Authenticator) Collect() []*commonpb.Metric {
	return append(m.store.Collect(), m.collectPresented(time.Now())...)
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

// recordPresented notes that the user presented the certificate at the
// given time.
func (m *Authenticator) recordPresented(user string, leaf *x509.Certificate, now time.Time) {
	id := metrics.MetricID{
		Name: metricClientCertificateNotAfter,
		Labels: metrics.Labels{
			labelUser:     user,
			labelNotAfter: strconv.FormatInt(leaf.NotAfter.Unix(), 10),
		},
	}

	// Recordings share the lock, and an eviction waits for every one in
	// flight to have stamped its entry.
	m.presentedMu.RLock()
	defer m.presentedMu.RUnlock()

	presented := m.presented.GetOrCreate(id, func() *presentedCertificate {
		return &presentedCertificate{NotAfter: leaf.NotAfter}
	})
	presented.LastSeen.Store(now.UnixNano())
}

// collectPresented reports for every user the earliest expiry among the
// certificates the user presented within the retention window.
//
// A certificate unused for longer is dropped first, so a user whose every
// connection presents a renewed certificate moves on to its expiry once
// the window has passed.
func (m *Authenticator) collectPresented(now time.Time) []*commonpb.Metric {
	m.evictPresented(now.Add(-presentedRetention))

	earliest := map[string]time.Time{}
	for _, entry := range m.presented.Metrics() {
		user := entry.ID.Labels[labelUser]
		if notAfter, ok := earliest[user]; !ok || entry.Value.NotAfter.Before(notAfter) {
			earliest[user] = entry.Value.NotAfter
		}
	}

	out := make([]*commonpb.Metric, 0, len(earliest))
	for _, user := range slices.Sorted(maps.Keys(earliest)) {
		out = append(out, commonpb.NewMetricGauge(
			metricClientCertificateNotAfter,
			float64(earliest[user].Unix()),
			commonpb.NewLabel(labelUser, user),
		))
	}

	return out
}

// evictPresented drops the certificates last presented before the cutoff,
// excluding every recording while it does.
func (m *Authenticator) evictPresented(cutoff time.Time) {
	m.presentedMu.Lock()
	defer m.presentedMu.Unlock()

	before := cutoff.UnixNano()
	m.presented.DeleteWhere(func(_ metrics.MetricID, presented *presentedCertificate) bool {
		return presented.LastSeen.Load() < before
	})
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
