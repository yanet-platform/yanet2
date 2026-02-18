package sshkey

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
)

// Authenticator implements SSH key authentication.
//
// It verifies tokens signed with SSH private keys against known public
// keys, with method binding and timestamp-based replay protection.
type Authenticator struct {
	keyStore   *KeyStore
	timeWindow time.Duration
	log        *zap.Logger
}

type authenticatorOptions struct {
	Log        *zap.Logger
	TimeWindow time.Duration
}

func newAuthenticatorOptions() *authenticatorOptions {
	return &authenticatorOptions{
		Log:        zap.NewNop(),
		TimeWindow: defaultTimeWindow,
	}
}

// Option configures the Authenticator.
type Option func(*authenticatorOptions)

// WithLog sets the logger.
func WithLog(log *zap.Logger) Option {
	return func(o *authenticatorOptions) {
		o.Log = log
	}
}

// WithTimeWindow sets the timestamp tolerance window.
func WithTimeWindow(d time.Duration) Option {
	return func(o *authenticatorOptions) {
		o.TimeWindow = d
	}
}

// NewAuthenticator creates a new SSH key Authenticator.
func NewAuthenticator(keyStore *KeyStore, opts ...Option) *Authenticator {
	options := newAuthenticatorOptions()
	for _, o := range opts {
		o(options)
	}

	return &Authenticator{
		keyStore:   keyStore,
		timeWindow: options.TimeWindow,
		log:        options.Log,
	}
}

// Name returns the authenticator name for logging.
func (m *Authenticator) Name() string {
	return "sshkey"
}

// IsTokenSupported checks if the token has the "sshkey " prefix.
func (m *Authenticator) IsTokenSupported(token string) bool {
	return strings.HasPrefix(strings.ToLower(token), tokenPrefix)
}

// Authenticate validates the SSH key token and returns authentication info.
//
// Verification steps:
//  1. Parse and validate token fields.
//  2. Check timestamp window (replay protection).
//  3. Check method binding (token.Method == reqInfo.FullMethod).
//  4. Look up public keys for the username.
//  5. Verify SSH signature against known keys.
//  6. Return AuthInfo with the username.
func (m *Authenticator) Authenticate(
	ctx context.Context,
	rawToken string,
	reqInfo *core.RequestInfo,
) (*core.AuthInfo, error) {
	token, err := parseToken(rawToken)
	if err != nil {
		return nil, status.Errorf(
			codes.Unauthenticated, "invalid sshkey token: %v", err,
		)
	}

	if err := token.checkTimestamp(time.Now(), m.timeWindow); err != nil {
		return nil, status.Errorf(
			codes.Unauthenticated, "sshkey token expired: %v", err,
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

	publicKeys := m.keyStore.GetKeys(token.Username)
	if len(publicKeys) == 0 {
		return nil, status.Errorf(
			codes.Unauthenticated,
			"no SSH keys found for user %q", token.Username,
		)
	}

	signedData := token.canonicalSignedData()
	if err := verifySignature(signedData, token.Signature, publicKeys); err != nil {
		return nil, status.Errorf(
			codes.Unauthenticated, "signature verification failed: %v", err,
		)
	}

	m.log.Debug("sshkey authentication successful",
		zap.String("username", token.Username),
		zap.String("method", token.Method),
	)

	return &core.AuthInfo{
		Username:   token.Username,
		AuthMethod: "sshkey",
	}, nil
}

// KeyStore returns the underlying key store for external configuration
// (e.g. manager wiring).
func (m *Authenticator) KeyStore() *KeyStore {
	return m.keyStore
}
