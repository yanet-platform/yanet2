package auth_test

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
)

func TestManager_Authenticate(t *testing.T) {
	config := &auth.Config{
		Disabled: true,
	}
	m, err := auth.NewManager(config)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	ctx := t.Context()

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
	m, err := auth.NewManager(&auth.Config{
		Disabled: true,
	}, auth.WithLog(log))
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	ctx := t.Context()

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

// writeIdentitiesFile writes an identities.yaml file with alice (active,
// operators group) and mallory (disabled, operators group).
func writeIdentitiesFile(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "identities.yaml")
	content := "identities:\n" +
		"  - username: alice\n" +
		"    groups: [operators]\n" +
		"  - username: mallory\n" +
		"    groups: [operators]\n" +
		"    disabled: true\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	return path
}

// writePermissionsFile writes a permissions.yaml file granting the
// operators group access to every method of test.Service.
func writePermissionsFile(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "permissions.yaml")
	content := "permissions:\n" +
		"  groups:\n" +
		"    - name: operators\n" +
		"      permissions:\n" +
		"        - \"/test.Service/*\"\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	return path
}

// writeBasicCredentialsFile writes a basic_auth.yaml file with bcrypt
// hashes for alice, mallory, and ghost, all sharing the same password.
func writeBasicCredentialsFile(t *testing.T) string {
	t.Helper()

	hash, err := bcrypt.GenerateFromPassword([]byte("s3cret"), bcrypt.MinCost)
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "basic_auth.yaml")
	content := "credentials:\n" +
		"  - username: alice\n" +
		"    password_hash: " + string(hash) + "\n" +
		"  - username: mallory\n" +
		"    password_hash: " + string(hash) + "\n" +
		"  - username: ghost\n" +
		"    password_hash: " + string(hash) + "\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	return path
}

// basicToken builds a "Basic <base64(username:password)>" token.
func basicToken(username, password string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
}

// buildEnabledConfig unmarshals real YAML into auth.Config, wiring a file
// identity provider, an RBAC permission store, and a basic authenticator, the
// same way production configuration is parsed.
func buildEnabledConfig(t *testing.T) auth.Config {
	t.Helper()

	identitiesPath := writeIdentitiesFile(t)
	permissionsPath := writePermissionsFile(t)
	credentialsPath := writeBasicCredentialsFile(t)

	text := "disabled: false\n" +
		"identity_providers:\n" +
		"  - type: file\n" +
		"    path: " + identitiesPath + "\n" +
		"permissions_path: " + permissionsPath + "\n" +
		"authenticators:\n" +
		"  - type: basic\n" +
		"    config:\n" +
		"      credentials_path: " + credentialsPath + "\n"

	var config auth.Config
	require.NoError(t, yaml.Unmarshal([]byte(text), &config))

	return config
}

// TestNewManagerEnabled verifies that an enabled Manager, built entirely
// from YAML configuration, wires the basic authenticator factory and the
// file identity provider into a working authentication and authorization
// path.
func TestNewManagerEnabled(t *testing.T) {
	config := buildEnabledConfig(t)

	manager, err := auth.NewManager(&config)
	require.NoError(t, err)

	requestInfo := &core.RequestInfo{FullMethod: "/test.Service/Method"}

	t.Run("known active user authenticates with group and method", func(t *testing.T) {
		principal, err := manager.Authenticate(t.Context(), basicToken("alice", "s3cret"), requestInfo)
		require.NoError(t, err)
		assert.Equal(t, "alice", principal.User)
		assert.Equal(t, []string{"operators"}, principal.Groups)
		assert.Equal(t, "basic", principal.AuthMethod)
		assert.False(t, principal.IsAnonymous)

		require.NoError(t, manager.Authorize(t.Context(), principal, "/test.Service/Method"))

		err = manager.Authorize(t.Context(), principal, "/other.Service/Method")
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.PermissionDenied, st.Code())
	})

	t.Run("unsupported token falls through to none authenticator", func(t *testing.T) {
		principal, err := manager.Authenticate(t.Context(), "bearer whatever", requestInfo)
		require.NoError(t, err)
		assert.True(t, principal.IsAnonymous)
		assert.Equal(t, "none", principal.AuthMethod)

		err = manager.Authorize(t.Context(), principal, requestInfo.FullMethod)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.PermissionDenied, st.Code())
	})

	t.Run("disabled identity fails authentication", func(t *testing.T) {
		_, err := manager.Authenticate(t.Context(), basicToken("mallory", "s3cret"), requestInfo)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Unauthenticated, st.Code())
		assert.Contains(t, st.Message(), `"mallory" is disabled`)
	})

	t.Run("valid credentials without an identity entry fail authentication", func(t *testing.T) {
		_, err := manager.Authenticate(t.Context(), basicToken("ghost", "s3cret"), requestInfo)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Unauthenticated, st.Code())
		assert.Contains(t, st.Message(), "identity lookup failed")
	})
}

// TestNewManagerConfigErrors verifies that NewManager rejects invalid
// configurations, including binding failures inside the per-type
// authenticator factories registered in the factory registry.
func TestNewManagerConfigErrors(t *testing.T) {
	validConfig := buildEnabledConfig(t)

	tests := []struct {
		name        string
		mutate      func(config auth.Config) auth.Config
		errContains []string
	}{
		{
			name: "unknown authenticator type",
			mutate: func(config auth.Config) auth.Config {
				config.Authenticators = []auth.AuthenticatorConfig{
					{Type: "unknown-method"},
				}
				return config
			},
			errContains: []string{"unknown-method"},
		},
		{
			name: "unknown identity provider type",
			mutate: func(config auth.Config) auth.Config {
				config.IdentityProviders = []auth.IdentityProviderConfig{
					{Type: "ldap"},
				}
				return config
			},
			errContains: []string{"ldap"},
		},
		{
			name: "no identity providers configured",
			mutate: func(config auth.Config) auth.Config {
				config.IdentityProviders = nil
				return config
			},
			errContains: []string{"no identity providers configured"},
		},
		{
			name: "permissions_path pointing at a nonexistent file",
			mutate: func(config auth.Config) auth.Config {
				config.PermissionsPath = "/nonexistent/permissions.yaml"
				return config
			},
			errContains: []string{"permissions"},
		},
		{
			name: "sshkey registry binding surfaces the inner factory error",
			mutate: func(config auth.Config) auth.Config {
				var configNode yaml.Node
				require.NoError(t, yaml.Unmarshal([]byte("{}\n"), &configNode))
				config.Authenticators = []auth.AuthenticatorConfig{
					{Type: "sshkey", Config: configNode},
				}
				return config
			},
			errContains: []string{"sshkey", "keys_path is required"},
		},
		{
			name: "sshcert registry binding surfaces the inner factory error",
			mutate: func(config auth.Config) auth.Config {
				var configNode yaml.Node
				require.NoError(t, yaml.Unmarshal([]byte("{}\n"), &configNode))
				config.Authenticators = []auth.AuthenticatorConfig{
					{Type: "sshcert", Config: configNode},
				}
				return config
			},
			errContains: []string{"sshcert", "ca_sources is required"},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			config := testCase.mutate(validConfig)

			_, err := auth.NewManager(&config)
			require.Error(t, err)
			for _, want := range testCase.errContains {
				assert.Contains(t, err.Error(), want)
			}
		})
	}
}
