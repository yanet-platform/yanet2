package sshcert

import (
	"context"
	"strings"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
)

const (
	// defaultTimeWindow is the default timestamp tolerance window.
	defaultTimeWindow = 5 * time.Second
	// defaultRefreshInterval is the default interval for periodic reload of
	// CA and KRL data.
	defaultRefreshInterval = 5 * time.Minute
)

// Authenticator implements SSH certificate authentication.
//
// It verifies tokens signed with SSH private keys whose corresponding
// certificates are signed by a trusted CA.
//
// Only ecdsa-sha2-nistp256 certificates are supported.
type Authenticator struct {
	caVerifier        CAVerifier
	revocationChecker RevocationChecker
	timeWindow        time.Duration
	refreshInterval   time.Duration
	stopCh            chan struct{}
	log               *zap.Logger
}

type authenticatorOptions struct {
	TimeWindow      time.Duration
	RefreshInterval time.Duration
	Log             *zap.Logger
}

func newAuthenticatorOptions() *authenticatorOptions {
	return &authenticatorOptions{
		TimeWindow:      defaultTimeWindow,
		RefreshInterval: defaultRefreshInterval,
		Log:             zap.NewNop(),
	}
}

// Option configures the Authenticator.
type Option func(*authenticatorOptions)

// WithTimeWindow sets the timestamp tolerance window.
func WithTimeWindow(d time.Duration) Option {
	return func(o *authenticatorOptions) {
		o.TimeWindow = d
	}
}

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

// NewAuthenticator creates a new SSH certificate Authenticator.
func NewAuthenticator(
	caVerifier CAVerifier,
	revocationChecker RevocationChecker,
	opts ...Option,
) *Authenticator {
	options := newAuthenticatorOptions()
	for _, o := range opts {
		o(options)
	}

	a := &Authenticator{
		caVerifier:        caVerifier,
		revocationChecker: revocationChecker,
		timeWindow:        options.TimeWindow,
		refreshInterval:   options.RefreshInterval,
		log:               options.Log,
		stopCh:            make(chan struct{}),
	}

	// Start periodic refresh.
	go a.refreshLoop()

	return a
}

// Name returns the authenticator name for logging.
func (m *Authenticator) Name() string {
	return "sshcert"
}

// IsTokenSupported checks if the token has the "sshcert " prefix.
func (m *Authenticator) IsTokenSupported(token string) bool {
	return strings.HasPrefix(strings.ToLower(token), tokenPrefix)
}

// Authenticate validates the SSH certificate token and returns authentication
// info.
//
// Verification steps:
//  1. Parse and validate token fields.
//  2. Check timestamp window (replay protection).
//  3. Check method binding.
//  4. Parse SSH certificate from token.
//  5. Check key type (ecdsa-sha2-nistp256 only).
//  6. Check certificate type (UserCert).
//  7. Check certificate validity period.
//  8. Verify CA signature.
//  9. Check KRL.
//  10. Extract username from principals.
//  11. Verify token signature.
//  12. Return AuthInfo with the username.
func (m *Authenticator) Authenticate(
	ctx context.Context,
	rawToken string,
	reqInfo *core.RequestInfo,
) (*core.AuthInfo, error) {
	token, err := parseToken(rawToken)
	if err != nil {
		return nil, status.Errorf(
			codes.Unauthenticated,
			"invalid sshcert token: %v", err,
		)
	}

	if err := token.checkTimestamp(time.Now(), m.timeWindow); err != nil {
		return nil, status.Errorf(
			codes.Unauthenticated,
			"sshcert token expired: %v", err,
		)
	}

	if reqInfo != nil && reqInfo.FullMethod != "" {
		if token.Method != reqInfo.FullMethod {
			return nil, status.Errorf(
				codes.Unauthenticated,
				"method binding mismatch: token method %q != request method %q",
				token.Method, reqInfo.FullMethod,
			)
		}
	}

	cert, err := parseCertificate(token.Certificate)
	if err != nil {
		return nil, status.Errorf(
			codes.Unauthenticated,
			"invalid certificate: %v", err,
		)
	}

	if err := checkKeyType(cert); err != nil {
		return nil, status.Errorf(
			codes.Unauthenticated,
			"certificate key type check failed: %v", err,
		)
	}

	if err := checkCertType(cert); err != nil {
		return nil, status.Errorf(
			codes.Unauthenticated,
			"certificate type check failed: %v", err,
		)
	}

	if err := checkValidity(cert, time.Now()); err != nil {
		return nil, status.Errorf(
			codes.Unauthenticated,
			"certificate validity check failed: %v", err,
		)
	}

	if err := m.caVerifier.VerifyCA(cert); err != nil {
		return nil, status.Errorf(
			codes.Unauthenticated,
			"CA verification failed: %v", err,
		)
	}

	if err := m.revocationChecker.IsRevoked(cert); err != nil {
		return nil, status.Errorf(
			codes.Unauthenticated,
			"certificate revocation check failed: %v", err,
		)
	}

	username, err := extractPrincipal(cert)
	if err != nil {
		return nil, status.Errorf(
			codes.Unauthenticated,
			"failed to extract principal: %v", err,
		)
	}

	signedData := token.canonicalSignedData()
	if err := verifySignature(signedData, token.Signature, cert); err != nil {
		return nil, status.Errorf(
			codes.Unauthenticated,
			"signature verification failed: %v", err,
		)
	}

	m.log.Debug("sshcert authentication successful",
		zap.String("username", username),
		zap.String("method", token.Method),
		zap.Uint64("cert_serial", cert.Serial),
	)

	return &core.AuthInfo{
		Username:   username,
		AuthMethod: "sshcert",
	}, nil
}

// Close stops the periodic refresh goroutine.
func (m *Authenticator) Close() {
	close(m.stopCh)
}

// refreshLoop periodically reloads CA and KRL data.
func (m *Authenticator) refreshLoop() {
	ticker := time.NewTicker(m.refreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.doRefresh()
		}
	}
}

// doRefresh reloads CA and KRL data.
func (m *Authenticator) doRefresh() {
	if err := m.caVerifier.Reload(); err != nil {
		m.log.Warn("failed to refresh CA store", zap.Error(err))
	} else {
		m.log.Info("refreshed CA store")
	}

	if err := m.revocationChecker.Reload(); err != nil {
		m.log.Warn("failed to refresh KRL", zap.Error(err))
	} else {
		m.log.Info("refreshed KRL")
	}
}
