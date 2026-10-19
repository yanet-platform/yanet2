package auth_test

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth"
)

type gatewayAuthConfig struct {
	Gateway struct {
		Auth auth.Config `yaml:"auth"`
	} `yaml:"gateway"`
}

// authConfigEnv isolates policy overrides while preserving their production names.
func authConfigEnv(t *testing.T, environment map[string]string) xcfg.Option {
	t.Helper()
	for _, entry := range os.Environ() {
		name, value, found := strings.Cut(entry, "=")
		if !found || !strings.HasPrefix(name, "YANET_GATEWAY_AUTH_") {
			continue
		}
		require.NoError(t, os.Unsetenv(name))
		t.Cleanup(func() {
			require.NoError(t, os.Setenv(name, value))
		})
	}
	for name, value := range environment {
		t.Setenv(name, value)
	}
	return xcfg.WithEnv()
}

// Test_Config_StrictPolicies verifies that omitted and deprecated policies resolve
// to one canonical mode, and malformed or ambiguous input cannot select a policy.
func Test_Config_StrictPolicies(t *testing.T) {
	cases := []struct {
		name       string
		input      string
		mode       auth.Mode
		errorMatch string
	}{
		{name: "omitted policy defaults to disabled", input: "{}", mode: auth.ModeDisabled},
		{name: "empty block defaults to disabled", input: "gateway: {auth: {}}", mode: auth.ModeDisabled},
		{name: "canonical disabled", input: "gateway: {auth: {mode: disabled}}", mode: auth.ModeDisabled},
		{name: "canonical audit", input: "gateway: {auth: {mode: audit}}", mode: auth.ModeAudit},
		{name: "canonical enabled", input: "gateway: {auth: {mode: enabled}}", mode: auth.ModeEnabled},
		{name: "legacy true means disabled", input: "gateway: {auth: {disabled: true}}", mode: auth.ModeDisabled},
		{name: "legacy false means enabled", input: "gateway: {auth: {disabled: false}}", mode: auth.ModeEnabled},
		{name: "matching enabled policies conflict", input: "gateway: {auth: {mode: enabled, disabled: false}}", errorMatch: "cannot both be specified"},
		{name: "opposing policies conflict", input: "gateway: {auth: {mode: enabled, disabled: true}}", errorMatch: "cannot both be specified"},
		{name: "unknown mode is rejected", input: "gateway: {auth: {mode: permissive}}", errorMatch: "unknown mode"},
		{name: "empty mode is rejected", input: "gateway: {auth: {mode: ''}}", errorMatch: "mode"},
		{name: "null mode is rejected", input: "gateway: {auth: {mode: null}}", errorMatch: "mode"},
		{name: "null legacy boolean is rejected", input: "gateway: {auth: {disabled: null}}", errorMatch: "disabled"},
		{name: "quoted legacy boolean is rejected", input: "gateway: {auth: {disabled: 'false'}}", errorMatch: "into bool"},
		{name: "unknown auth key is rejected", input: "gateway: {auth: {mdoe: enabled}}", errorMatch: "gateway.auth.mdoe"},
		{name: "unknown authenticator key is rejected", input: "gateway: {auth: {authenticators: [{type: sshcert, bogus: true}]}}", errorMatch: "gateway.auth.authenticators[0].bogus"},
		{
			name: "SSH certificate audit needs no identity or permissions configuration",
			input: "gateway:\n  auth:\n    mode: audit\n    authenticators:\n" +
				"      - type: sshcert\n        config:\n" +
				"          ca_sources: [/etc/yanet2/auth/ssh-ca.pub]\n",
			mode: auth.ModeAudit,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var config gatewayAuthConfig
			config.Gateway.Auth = auth.DefaultConfig()
			err := xcfg.Decode([]byte(tc.input), &config, xcfg.WithKnownFields())
			if tc.errorMatch != "" {
				require.ErrorContains(t, err, tc.errorMatch)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.mode, config.Gateway.Auth.Mode)
			require.Nil(t, config.Gateway.Auth.Disabled)
		})
	}
}

// Test_Config_EnvironmentPolicies verifies that production environment aliases
// use the same normalization and conflict checks as strict file configuration.
func Test_Config_EnvironmentPolicies(t *testing.T) {
	cases := []struct {
		name        string
		input       string
		environment map[string]string
		mode        auth.Mode
		errorMatch  string
	}{
		{name: "no override retains disabled default", input: "{}", mode: auth.ModeDisabled},
		{name: "environment selects disabled", input: "{}", environment: map[string]string{"YANET_GATEWAY_AUTH_MODE": "disabled"}, mode: auth.ModeDisabled},
		{name: "environment selects audit", input: "{}", environment: map[string]string{"YANET_GATEWAY_AUTH_MODE": "audit"}, mode: auth.ModeAudit},
		{name: "environment selects enabled", input: "{}", environment: map[string]string{"YANET_GATEWAY_AUTH_MODE": "enabled"}, mode: auth.ModeEnabled},
		{name: "legacy environment true selects disabled", input: "{}", environment: map[string]string{"YANET_GATEWAY_AUTH_DISABLED": "true"}, mode: auth.ModeDisabled},
		{name: "legacy environment false selects enabled", input: "{}", environment: map[string]string{"YANET_GATEWAY_AUTH_DISABLED": "false"}, mode: auth.ModeEnabled},
		{name: "environment overrides same canonical key", input: "gateway: {auth: {mode: enabled}}", environment: map[string]string{"YANET_GATEWAY_AUTH_MODE": "audit"}, mode: auth.ModeAudit},
		{name: "environment overrides same legacy key", input: "gateway: {auth: {disabled: true}}", environment: map[string]string{"YANET_GATEWAY_AUTH_DISABLED": "false"}, mode: auth.ModeEnabled},
		{name: "matching environment policies conflict", input: "{}", environment: map[string]string{"YANET_GATEWAY_AUTH_MODE": "enabled", "YANET_GATEWAY_AUTH_DISABLED": "false"}, errorMatch: "cannot both be specified"},
		{name: "canonical environment conflicts with matching legacy file", input: "gateway: {auth: {disabled: true}}", environment: map[string]string{"YANET_GATEWAY_AUTH_MODE": "disabled"}, errorMatch: "cannot both be specified"},
		{name: "legacy environment conflicts with matching canonical file", input: "gateway: {auth: {mode: enabled}}", environment: map[string]string{"YANET_GATEWAY_AUTH_DISABLED": "false"}, errorMatch: "cannot both be specified"},
		{name: "unknown environment mode is rejected", input: "{}", environment: map[string]string{"YANET_GATEWAY_AUTH_MODE": "permissive"}, errorMatch: "unknown mode"},
		{name: "empty environment mode is rejected", input: "{}", environment: map[string]string{"YANET_GATEWAY_AUTH_MODE": ""}, errorMatch: "mode"},
		{name: "null legacy environment is rejected", input: "{}", environment: map[string]string{"YANET_GATEWAY_AUTH_DISABLED": "null"}, errorMatch: "disabled"},
		{name: "invalid legacy environment is rejected", input: "{}", environment: map[string]string{"YANET_GATEWAY_AUTH_DISABLED": "sometimes"}, errorMatch: "into bool"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var config gatewayAuthConfig
			config.Gateway.Auth = auth.DefaultConfig()
			err := xcfg.Decode(
				[]byte(tc.input), &config,
				xcfg.WithKnownFields(), authConfigEnv(t, tc.environment),
			)
			if tc.errorMatch != "" {
				require.ErrorContains(t, err, tc.errorMatch)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.mode, config.Gateway.Auth.Mode)
			require.Nil(t, config.Gateway.Auth.Disabled)
		})
	}
}

// Test_Config_LegacyWarningNamesReplacement verifies that normalized legacy
// configuration still produces an actionable migration warning at startup.
func Test_Config_LegacyWarningNamesReplacement(t *testing.T) {
	for _, tc := range []struct {
		name        string
		input       string
		environment map[string]string
	}{
		{name: "legacy YAML", input: "gateway: {auth: {disabled: true}}"},
		{name: "legacy environment", input: "{}", environment: map[string]string{"YANET_GATEWAY_AUTH_DISABLED": "true"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var config gatewayAuthConfig
			config.Gateway.Auth = auth.DefaultConfig()
			require.NoError(t, xcfg.Decode(
				[]byte(tc.input), &config,
				xcfg.WithKnownFields(), authConfigEnv(t, tc.environment),
			))
			logCore, logs := observer.New(zap.WarnLevel)
			_, err := auth.NewManager(&config.Gateway.Auth, auth.WithLog(zap.New(logCore)))
			require.NoError(t, err)
			warnings := logs.FilterMessageSnippet("deprecated").All()
			require.Len(t, warnings, 1)
			require.Contains(t, warnings[0].Message, "gateway.auth.disabled")
			require.Contains(t, warnings[0].Message, "gateway.auth.mode")
		})
	}
}

// Test_Config_CanonicalRedecodeClearsLegacyWarning verifies that reusing a
// configuration with canonical input does not retain a prior migration warning.
func Test_Config_CanonicalRedecodeClearsLegacyWarning(t *testing.T) {
	config := auth.DefaultConfig()
	require.NoError(t, xcfg.Decode([]byte("disabled: true"), &config, xcfg.WithKnownFields()))
	require.NoError(t, xcfg.Decode([]byte("mode: disabled"), &config, xcfg.WithKnownFields()))
	logCore, logs := observer.New(zap.WarnLevel)
	_, err := auth.NewManager(&config, auth.WithLog(zap.New(logCore)))
	require.NoError(t, err)
	require.Empty(t, logs.FilterMessageSnippet("deprecated").All())
}
