package auth

import (
	"context"
	"crypto/rand"

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
	principal, err := admitCall(ctx, m, m.log, credential, fullMethod)
	if err != nil {
		return nil, "", nil, err
	}

	id := rand.Text()
	m.admissions.Store(id, admission{Credential: credential, Principal: principal})

	return principal, id, func() { m.admissions.Delete(id) }, nil
}

// TakeAdmission takes the admission parked under id, which no later call can
// take again, and reports whether there was one.
func (m *Manager) TakeAdmission(id string) (core.Credential, *core.Principal, bool) {
	value, ok := m.admissions.LoadAndDelete(id)
	if !ok {
		return core.Credential{}, nil, false
	}
	parked := value.(admission)

	return parked.Credential, parked.Principal, true
}

// admitIncoming admits a call that reached a server and returns the call's
// context with its principal.
//
// A call is admitted by the admission its metadata names when it carries
// one, else on the credential it presents. An admission is taken once and
// authorized again for the method the call reached.
func admitIncoming(ctx context.Context, manager *Manager, log *zap.Logger, fullMethod string) (context.Context, error) {
	ids := metadata.ValueFromIncomingContext(ctx, AdmissionMetadataKey)
	if len(ids) == 0 {
		principal, err := admitCall(ctx, manager, log, ExtractCredential(ctx), fullMethod)
		if err != nil {
			return nil, err
		}

		return core.WithPrincipal(ctx, principal), nil
	}

	credential, principal, ok := manager.TakeAdmission(ids[0])
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "authentication failed: unknown admission")
	}
	if err := authorizeCall(ctx, manager, log, principal, fullMethod); err != nil {
		return nil, err
	}

	return WithCredential(core.WithPrincipal(ctx, principal), credential), nil
}

// admitCall authenticates a call on its credential and authorizes its
// principal for the method.
//
// It fails with Unauthenticated or PermissionDenied.
func admitCall(
	ctx context.Context,
	manager *Manager,
	log *zap.Logger,
	credential core.Credential,
	fullMethod string,
) (*core.Principal, error) {
	principal, err := manager.AuthenticateRequest(ctx, credential, &core.RequestInfo{FullMethod: fullMethod})
	if err != nil {
		log.Warn("authentication failed",
			zap.String("method", fullMethod),
			zap.Error(err),
		)
		return nil, status.Errorf(codes.Unauthenticated, "authentication failed: %v", err)
	}

	if err := authorizeCall(ctx, manager, log, principal, fullMethod); err != nil {
		return nil, err
	}

	return principal, nil
}

// authorizeCall authorizes a principal for a method, failing with
// PermissionDenied.
func authorizeCall(ctx context.Context, manager *Manager, log *zap.Logger, principal *core.Principal, fullMethod string) error {
	if err := manager.Authorize(ctx, principal, fullMethod); err != nil {
		log.Warn("authorization failed",
			zap.String("method", fullMethod),
			zap.String("user", principal.User),
			zap.Error(err),
		)
		return status.Errorf(codes.PermissionDenied, "authorization failed: %v", err)
	}

	return nil
}
