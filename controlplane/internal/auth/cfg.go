package auth

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// Mode selects RPC authentication and authorization policy.
type Mode string

const (
	ModeDisabled Mode = "disabled"
	ModeAudit    Mode = "audit"
	ModeEnabled  Mode = "enabled"
)

// Config is the configuration for authentication and authorization.
type Config struct {
	// Mode defaults to disabled when no policy is supplied.
	Mode Mode `yaml:"mode"`
	// IdentityProviders is a list of identity providers (chain of responsibility).
	// First match wins.
	IdentityProviders []IdentityProviderConfig `yaml:"identity_providers"`
	// Authenticators is a list of authenticator configurations.
	//
	// Each entry specifies a type and its type-specific config.
	Authenticators []AuthenticatorConfig `yaml:"authenticators"`
	// PermissionsPath is the path to the permissions YAML file.
	PermissionsPath string `yaml:"permissions_path"`
}

// Validate rejects unsupported authentication modes.
func (m *Config) Validate() error {
	switch m.Mode {
	case ModeDisabled, ModeAudit, ModeEnabled:
		return nil
	default:
		return fmt.Errorf("unknown mode %q; use disabled, audit, or enabled", m.Mode)
	}
}

// IdentityProviderConfig configures a single identity provider.
type IdentityProviderConfig struct {
	// Type is the provider type: "file", "pam" (future), etc.
	Type string `yaml:"type"`
	// Path is the file path (for file-based providers).
	Path string `yaml:"path"`
}

// AuthenticatorConfig is a generic authenticator configuration entry.
//
// The Type field selects the authenticator factory, and Config holds the
// type-specific YAML configuration decoded by the factory.
type AuthenticatorConfig struct {
	// Type is the authenticator type name.
	Type string `yaml:"type"`
	// Config is the raw YAML node for type-specific configuration.
	Config yaml.Node `yaml:"config"`
}

// DefaultConfig returns the default authentication configuration.
func DefaultConfig() Config {
	return Config{
		Mode: ModeDisabled,
	}
}
