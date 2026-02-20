package auth

import (
	"context"
	"testing"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
)

func TestManager_Authenticate(t *testing.T) {
	cfg := &Config{
		Disabled: true,
	}
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	ctx := context.Background()

	tests := []struct {
		name  string
		token string
	}{
		{
			name:  "empty token",
			token: "",
		},
		{
			name:  "with token",
			token: "some-token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requestInfo := &core.RequestInfo{FullMethod: "/test.Service/Method"}
			principal, err := m.Authenticate(ctx, tt.token, requestInfo)
			if err != nil {
				t.Fatalf("Authenticate() error = %v, want nil", err)
			}

			if principal == nil {
				t.Fatal("Authenticate() returned nil principal")
			}

			if principal.User != "anonymous" {
				t.Errorf("principal.User = %q, want %q", principal.User, "anonymous")
			}

			if principal.AuthMethod != "none" {
				t.Errorf("principal.AuthMethod = %q, want %q", principal.AuthMethod, "none")
			}

			if !principal.IsAnonymous {
				t.Error("principal.IsAnonymous = false, want true")
			}
		})
	}
}

func TestManager_Authorize(t *testing.T) {
	log := zap.NewNop()
	m, err := NewManager(&Config{
		Disabled: true,
	}, WithLog(log))
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	ctx := context.Background()

	// Create a test principal.
	requestInfo := &core.RequestInfo{FullMethod: "/test.Service/Method"}
	principal, err := m.Authenticate(ctx, "", requestInfo)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}

	tests := []struct {
		name       string
		fullMethod string
		wantErr    bool
	}{
		{
			name:       "any method allowed in skeleton",
			fullMethod: "/routepb.RouteService/ListRoutes",
			wantErr:    false,
		},
		{
			name:       "another method",
			fullMethod: "/balancerpb.BalancerService/GetConfig",
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.Authorize(ctx, principal, tt.fullMethod)
			if (err != nil) != tt.wantErr {
				t.Errorf("Authorize() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
