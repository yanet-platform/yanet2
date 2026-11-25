package auth

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/tap"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
)

var errPolicyRevoked = status.Error(codes.PermissionDenied, "stream permission revoked")

type streamControlKey struct{}

type streamControl struct {
	Cancel context.CancelCauseFunc
}

type admittedStream struct {
	Method   string
	Identity *core.AuthInfo
}

// PolicyStreamTap gives the RPC transport a cancellable context.
//
// A derived interceptor context alone cannot unblock transport reads or
// writes. The tap runs before gRPC attaches the context to those operations.
func PolicyStreamTap(ctx context.Context, info *tap.Info) (context.Context, error) {
	ctx, cancel := context.WithCancelCause(ctx)
	return context.WithValue(ctx, streamControlKey{}, &streamControl{Cancel: cancel}), nil
}

func (m *Manager) trackStream(
	ctx context.Context, method string, identity *core.AuthInfo, policy *policySnapshot,
) (func(), error) {
	if m.managedPolicy == nil {
		return func() {}, nil
	}
	control, ok := ctx.Value(streamControlKey{}).(*streamControl)
	if !ok || identity == nil {
		return nil, status.Error(codes.Internal, "missing managed stream admission")
	}
	m.streamsMutex.Lock()
	defer m.streamsMutex.Unlock()
	if m.active.Load() != policy {
		return nil, status.Error(codes.Unavailable, "authorization changed before stream admission")
	}
	m.streams[control] = admittedStream{Method: method, Identity: identity}
	return func() { m.releaseStream(control) }, nil
}

func (m *Manager) releaseStream(control *streamControl) {
	m.streamsMutex.Lock()
	defer m.streamsMutex.Unlock()
	delete(m.streams, control)
}

func (m *Manager) publishPolicy(policy *policySnapshot) bool {
	m.streamsMutex.Lock()
	defer m.streamsMutex.Unlock()
	if policy.Sequence <= m.active.Load().Sequence {
		return false
	}
	ctx := context.Background()
	for control, stream := range m.streams {
		principal, err := m.buildPrincipal(ctx, policy, stream.Identity)
		allowedSSH := policy.SSHTrust == nil || stream.Identity.AuthMethod != "sshcert" ||
			policy.SSHTrust.TrustsSigner(stream.Identity.Subject.Assertion)
		if err != nil || !allowedSSH || policy.Authorize(ctx, principal, stream.Method) != nil {
			control.Cancel(errPolicyRevoked)
			delete(m.streams, control)
		}
	}
	// Report the revision only after sending cancellation to affected streams.
	policy.Activated = time.Now()
	m.active.Store(policy)
	return true
}
