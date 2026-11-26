package auth_test

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
)

func TestManager_Authenticate(t *testing.T) {
	config := &auth.Config{
		Mode: auth.ModeDisabled,
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
			principal, err := m.Authenticate(ctx, core.Credential{Token: tt.token}, requestInfo)
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
		Mode: auth.ModeDisabled,
	}, auth.WithLog(log))
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	ctx := t.Context()

	// Create a test principal.
	requestInfo := &core.RequestInfo{FullMethod: "/test.Service/Method"}
	principal, err := m.Authenticate(ctx, core.Credential{Token: ""}, requestInfo)
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

	text := "mode: enabled\n" +
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
		principal, err := manager.Authenticate(t.Context(), core.Credential{Token: basicToken("alice", "s3cret")}, requestInfo)
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
		principal, err := manager.Authenticate(t.Context(), core.Credential{Token: "bearer whatever"}, requestInfo)
		require.NoError(t, err)
		assert.True(t, principal.IsAnonymous)
		assert.Equal(t, "none", principal.AuthMethod)

		err = manager.Authorize(t.Context(), principal, requestInfo.FullMethod)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.PermissionDenied, st.Code())
	})

	t.Run("disabled identity fails authentication", func(t *testing.T) {
		_, err := manager.Authenticate(t.Context(), core.Credential{Token: basicToken("mallory", "s3cret")}, requestInfo)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Unauthenticated, st.Code())
		assert.Contains(t, st.Message(), `"mallory" is disabled`)
	})

	t.Run("valid credentials without an identity entry fail authentication", func(t *testing.T) {
		_, err := manager.Authenticate(t.Context(), core.Credential{Token: basicToken("ghost", "s3cret")}, requestInfo)
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

// Test_Manager_IdentityRefresh verifies that polling changes admissions while
// preserving static sources, permissions and the last valid identities.
func Test_Manager_IdentityRefresh(t *testing.T) {
	config := buildEnabledConfig(t)
	path := config.IdentityProviders[0].Path
	require.NoError(t, os.WriteFile(path, []byte("identities:\n  - username: alice\n    groups: [operators]\n"), 0o600))
	initial, err := os.ReadFile(path)
	require.NoError(t, err)
	config.IdentityProviders[0].RefreshInterval = time.Second
	dynamicPath := filepath.Join(t.TempDir(), "dynamic.yaml")
	require.NoError(t, os.WriteFile(dynamicPath, []byte("identities: []\n"), 0o600))
	config.IdentityProviders = append(config.IdentityProviders, auth.IdentityProviderConfig{
		Type: "file", Path: dynamicPath, RefreshInterval: time.Second,
	})
	staticPath := filepath.Join(t.TempDir(), "static.yaml")
	require.NoError(t, os.WriteFile(staticPath, []byte("identities: []\n"), 0o600))
	config.IdentityProviders = append(config.IdentityProviders, auth.IdentityProviderConfig{
		Type: "file", Path: staticPath,
	})
	encoded, err := yaml.Marshal(config)
	require.NoError(t, err)
	var decoded auth.Config
	require.NoError(t, xcfg.Decode(encoded, &decoded, xcfg.WithEnv()))
	logCore, logs := observer.New(zap.InfoLevel)
	manager, err := auth.NewManager(&decoded, auth.WithLog(zap.New(logCore)))
	require.NoError(t, err)

	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- manager.RunPolicyRefresh(ctx) }()
		defer func() {
			cancel()
			require.NoError(t, <-done)
		}()
		synctest.Wait()
		tick := func() {
			time.Sleep(time.Second)
			synctest.Wait()
		}
		admit := func(username string, code codes.Code) {
			_, _, release, err := manager.Admit(t.Context(), core.Credential{
				Token: basicToken(username, "s3cret"),
			}, "/test.Service/Method")
			require.Equal(t, code, status.Code(err), "%v", err)
			if release != nil {
				release()
			}
		}
		admit("ghost", codes.Unauthenticated)
		added := append(append([]byte{}, initial...), []byte("  - username: ghost\n    groups: [operators]\n")...)
		require.NoError(t, os.WriteFile(path, added, 0o600))
		// This file is intentionally static for this change.
		require.NoError(t, os.WriteFile(config.PermissionsPath, []byte("permissions: {}\n"), 0o600))
		tick()
		admit("ghost", codes.OK)
		admit("alice", codes.OK)

		before := logs.Len()
		tick()
		require.Equal(t, before, logs.Len())
		admit("mallory", codes.Unauthenticated)
		require.NoError(t, os.WriteFile(path, []byte("identities: not-a-list"), 0o600))
		require.NoError(t, os.WriteFile(dynamicPath, []byte("identities: [{username: mallory, groups: [operators]}]\n"), 0o600))
		tick()
		admit("ghost", codes.OK)
		admit("mallory", codes.OK)
		failures := logs.FilterMessage("failed to refresh policy source").All()
		require.Len(t, failures, 1)
		require.Equal(t, path, failures[0].ContextMap()["path"])
		tick()
		require.Equal(t, 1, logs.FilterMessage("failed to refresh policy source").Len())
		require.NoError(t, os.WriteFile(path, added, 0o600))
		tick()
		require.Equal(t, 1, logs.FilterMessage("recovered policy source").Len())
		tick()
		require.Equal(t, 1, logs.FilterMessage("recovered policy source").Len())

		disabled := append(append([]byte{}, added...), []byte("    disabled: true\n")...)
		require.NoError(t, os.WriteFile(path, disabled, 0o600))
		tick()
		admit("ghost", codes.Unauthenticated)
		require.NoError(t, os.WriteFile(staticPath, added, 0o600))
		require.NoError(t, os.WriteFile(path, initial, 0o600))
		tick()
		admit("ghost", codes.Unauthenticated)
		admit("alice", codes.OK)
	})
}

// Test_Manager_IdentityRefreshCancellation verifies that shutdown does not
// wait for a long polling interval, including cancellation before startup.
func Test_Manager_IdentityRefreshCancellation(t *testing.T) {
	config := buildEnabledConfig(t)
	config.IdentityProviders[0].RefreshInterval = time.Hour
	for _, alreadyCanceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "waiting worker", true: "canceled before start"}[alreadyCanceled], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				manager, err := auth.NewManager(&config)
				require.NoError(t, err)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				if alreadyCanceled {
					cancel()
				}
				start := time.Now()
				done := make(chan error, 1)
				go func() { done <- manager.RunPolicyRefresh(ctx) }()
				synctest.Wait()
				cancel()
				require.NoError(t, <-done)
				require.Equal(t, start, time.Now())
			})
		})
	}
}

// Test_Manager_IdentityRefreshStartup verifies that enabled mode rejects
// unusable initial identities while non-enforcing modes do not read them.
func Test_Manager_IdentityRefreshStartup(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mode    auth.Mode
		legacy  bool
		content string
		missing bool
		wantErr bool
	}{
		{name: "enabled malformed", mode: auth.ModeEnabled, content: "identities: not-a-list", wantErr: true},
		{name: "enabled invalid entry", mode: auth.ModeEnabled, content: "identities: [{groups: [operators]}]", wantErr: true},
		{name: "enabled missing", mode: auth.ModeEnabled, missing: true, wantErr: true},
		{name: "legacy enabled missing", legacy: true, missing: true, wantErr: true},
		{name: "audit ignores missing policies", mode: auth.ModeAudit, missing: true},
		{name: "disabled ignores missing policies", mode: auth.ModeDisabled, missing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := buildEnabledConfig(t)
			config.Mode = tc.mode
			if tc.legacy {
				disabled := false
				config.Disabled = &disabled
			}
			path := config.IdentityProviders[0].Path
			config.IdentityProviders[0].RefreshInterval = time.Second
			if tc.missing {
				require.NoError(t, os.Remove(path))
			} else {
				require.NoError(t, os.WriteFile(path, []byte(tc.content), 0o600))
			}
			if tc.mode == auth.ModeAudit || tc.mode == auth.ModeDisabled {
				require.NoError(t, os.Remove(config.PermissionsPath))
			}
			manager, err := auth.NewManager(&config)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.NoError(t, manager.RunPolicyRefresh(t.Context()))
		})
	}
}
