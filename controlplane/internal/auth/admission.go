package auth

import (
	"context"
	"crypto/rand"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
)

// AdmissionMetadataKey is the metadata key that names the admission a call
// was let in with before it reached a server.
const AdmissionMetadataKey = "x-yanet-admission"

// admission is a call let in before it reached a server.
type admission struct {
	// Credential is what the call presented.
	Credential core.Credential
	// Principal is who the call runs as.
	Principal *core.Principal
	Identity  *core.AuthInfo
	Method    string
	Revision  uint64
}

// Admit authenticates and authorizes a call to a method before the call
// reaches a server, and parks the outcome under a fresh id.
//
// The call carries the id to the server under AdmissionMetadataKey, and the
// server takes the admission once, as the call's credential and principal.
// The release forgets an admission no server took, and is safe to call after
// one did.
func (m *Manager) Admit(
	ctx context.Context,
	credential core.Credential,
	fullMethod string,
) (*core.Principal, string, func(), error) {
	policy := m.policyFor(ctx)
	principal, identity, err := m.admitCall(ctx, policy, credential, fullMethod)
	if err != nil {
		return nil, "", nil, err
	}

	id := rand.Text()
	m.admissions.Store(id, admission{
		Credential: credential,
		Principal:  principal,
		Identity:   identity,
		Method:     fullMethod,
		Revision:   policy.Revision(),
	})

	return principal, id, func() { m.admissions.Delete(id) }, nil
}

// admitIncoming admits a call that reached a server and returns the call's
// context with its principal.
//
// A call is admitted by the admission its metadata names when it carries
// one, else on the credential it presents. A parked admission is usable once
// for the original method while the checked policy is still active.
func (m *Manager) admitIncoming(
	ctx context.Context,
	fullMethod string,
	policy *policySnapshot,
) (context.Context, *core.AuthInfo, error) {
	ids := metadata.ValueFromIncomingContext(ctx, AdmissionMetadataKey)
	if len(ids) == 0 {
		principal, identity, err := m.admitCall(ctx, policy, ExtractCredential(ctx), fullMethod)
		if err != nil {
			return nil, nil, err
		}

		return core.WithPrincipal(ctx, principal), identity, nil
	}

	value, ok := m.admissions.LoadAndDelete(ids[0])
	if !ok {
		return nil, nil, status.Error(codes.Unauthenticated, "authentication failed: unknown admission")
	}
	parked := value.(admission)
	if parked.Method != fullMethod {
		return nil, nil, status.Error(codes.PermissionDenied, "admission is for another method")
	}
	if parked.Revision != policy.Revision() || m.active.Load() != policy {
		return nil, nil, status.Error(codes.Unavailable, "authorization changed before admission")
	}

	return WithCredential(core.WithPrincipal(ctx, parked.Principal), parked.Credential), parked.Identity, nil
}

// admitCall checks the credential and method against one policy.
//
// Only admission treats invalid credentials as anonymous in audit mode.
func (m *Manager) admitCall(
	ctx context.Context,
	policy *policySnapshot,
	credential core.Credential,
	fullMethod string,
) (*core.Principal, *core.AuthInfo, error) {
	principal, identity, err := m.authenticate(ctx, policy, credential, &core.RequestInfo{FullMethod: fullMethod})
	if err != nil {
		if m.mode != ModeAudit {
			m.log.Warn("authentication failed", zap.String("method", fullMethod), zap.Error(err))
			return nil, nil, status.Errorf(codes.Unauthenticated, "authentication failed: %v", err)
		}
		m.log.Warn("authentication failed in audit mode",
			zap.String("method", fullMethod),
			zap.String("reason", "invalid_credentials"),
		)
		principal = &core.Principal{
			User:        "anonymous",
			Groups:      []string{},
			AuthMethod:  "none",
			AuthTime:    time.Now(),
			IsAnonymous: true,
		}
	}

	if err := policy.Authorize(ctx, principal, fullMethod); err != nil {
		m.log.Warn("authorization failed",
			zap.String("method", fullMethod),
			zap.String("user", principal.User),
			zap.Error(err),
		)
		return nil, nil, status.Errorf(codes.PermissionDenied, "authorization failed: %v", err)
	}
	return principal, identity, nil
}
