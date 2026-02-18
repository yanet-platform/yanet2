package auth

import (
	"time"
)

// Config is the configuration for authentication and authorization.
type Config struct {
	// Disabled indicates if authentication is disabled.
	//
	// When true, all requests are treated as anonymous with full permissions.
	Disabled bool `yaml:"disabled"`
	// IdentityProviders is a list of identity providers (chain of responsibility).
	// First match wins.
	IdentityProviders []IdentityProviderConfig `yaml:"identity_providers"`
	// BasicAuth is the configuration for Basic Authentication.
	BasicAuth BasicAuthConfig `yaml:"basic_auth"`
	// SSHKey is the configuration for SSH Key Authentication.
	SSHKey SSHKeyConfig `yaml:"ssh_key"`
	// SSHCert is the configuration for SSH Certificate
	// Authentication.
	SSHCert SSHCertConfig `yaml:"ssh_cert"`
	// PermissionsPath is the path to the permissions YAML file.
	PermissionsPath string `yaml:"permissions_path"`
}

// IdentityProviderConfig configures a single identity provider.
type IdentityProviderConfig struct {
	// Type is the provider type: "file", "pam" (future), etc.
	Type string `yaml:"type"`
	// Path is the file path (for file-based providers).
	Path string `yaml:"path"`
}

// BasicAuthConfig configures Basic Authentication.
type BasicAuthConfig struct {
	// CredentialsPath is the path to the basic_auth.yaml file.
	CredentialsPath string `yaml:"credentials_path"`
}

// SSHKeyConfig configures SSH Key Authentication.
type SSHKeyConfig struct {
	// KeysPath is the path to the ssh_keys.yaml file.
	KeysPath string `yaml:"keys_path"`
	// TimeWindow is the timestamp tolerance window for replay protection.
	// Tokens with timestamps outside this window are rejected.
	//
	// Default: 5s.
	TimeWindow time.Duration `yaml:"time_window"`
}

// SSHCertConfig configures SSH Certificate Authentication.
type SSHCertConfig struct {
	// CASource is the path or URL to the CA public keys YAML file.
	//
	// Sources starting with "http://" or "https://" use HTTP, otherwise the
	// source is treated as a file path.
	CASource string `yaml:"ca_source"`
	// KRLSource is the path or URL to the OpenSSH KRL file (optional).
	//
	// Sources starting with "http://" or "https://" use HTTP, otherwise the
	// source is treated as a file path.
	KRLSource string `yaml:"krl_source"`
	// TimeWindow is the timestamp tolerance window for replay protection.
	//
	// Default: 5s.
	TimeWindow time.Duration `yaml:"time_window"`
	// RefreshInterval is the polling interval for reloading CA and KRL data
	// from their sources.
	//
	// Default: 5m.
	RefreshInterval time.Duration `yaml:"refresh_interval"`
}

// DefaultConfig returns the default authentication configuration.
func DefaultConfig() Config {
	return Config{
		Disabled: false, // Security by default.
		IdentityProviders: []IdentityProviderConfig{
			{
				Type: "file",
				Path: "/etc/yanet/identities.yaml",
			},
		},
		BasicAuth: BasicAuthConfig{
			CredentialsPath: "/etc/yanet/basic_auth.yaml",
		},
		PermissionsPath: "/etc/yanet/permissions.yaml",
	}
}
