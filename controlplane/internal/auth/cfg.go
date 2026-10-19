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
	// Disabled is the deprecated input alias for the authentication policy.
	Disabled *bool `yaml:"disabled,omitempty"`
	// IdentityProviders is a list of identity providers (chain of responsibility).
	// First match wins.
	IdentityProviders []IdentityProviderConfig `yaml:"identity_providers"`
	// Authenticators is a list of authenticator configurations.
	//
	// Each entry specifies a type and its type-specific config.
	Authenticators []AuthenticatorConfig `yaml:"authenticators"`
	// PermissionsPath is the path to the permissions YAML file.
	PermissionsPath string `yaml:"permissions_path"`
	legacyDisabled  bool
}

// UnmarshalYAML rejects ambiguous policies before normalizing legacy input.
func (m *Config) UnmarshalYAML(node *yaml.Node) error {
	var fields map[string]yaml.Node
	if err := node.Decode(&fields); err != nil {
		return err
	}
	mode, hasMode := fields["mode"]
	disabled, hasDisabled := fields["disabled"]
	if hasMode && hasDisabled {
		return fmt.Errorf("mode and deprecated disabled cannot both be specified; remove disabled and use mode")
	}
	if hasMode && (mode.ShortTag() == "!!null" || mode.Value == "") {
		return fmt.Errorf("mode must be disabled, audit, or enabled")
	}
	if hasDisabled && disabled.ShortTag() == "!!null" {
		return fmt.Errorf("deprecated disabled must be a boolean; use mode: disabled or mode: enabled")
	}

	type plainConfig Config
	decoded := plainConfig(*m)
	if err := node.Decode(&decoded); err != nil {
		return err
	}
	if hasMode && decoded.Mode == "" {
		return fmt.Errorf("mode must be disabled, audit, or enabled")
	}
	if hasDisabled {
		decoded.Mode = ModeEnabled
		if *decoded.Disabled {
			decoded.Mode = ModeDisabled
		}
		decoded.Disabled = nil
	}
	*m = Config(decoded)
	m.legacyDisabled = hasDisabled
	return m.Validate()
}

// Validate resolves an omitted policy and rejects unknown modes.
func (m *Config) Validate() error {
	if m.Disabled != nil {
		if m.Mode != "" {
			return fmt.Errorf("mode and deprecated disabled cannot both be specified; remove disabled and use mode")
		}
		m.Mode = ModeEnabled
		if *m.Disabled {
			m.Mode = ModeDisabled
		}
		m.Disabled = nil
		m.legacyDisabled = true
	}
	if m.Mode == "" {
		m.Mode = ModeDisabled
	}
	switch m.Mode {
	case ModeDisabled, ModeAudit, ModeEnabled:
		return nil
	default:
		return fmt.Errorf("unknown mode %q; use disabled, audit, or enabled", m.Mode)
	}
}

// LegacyPolicyUsed reports whether the deprecated policy spelling was supplied.
func (m *Config) LegacyPolicyUsed() bool {
	return m.legacyDisabled
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
