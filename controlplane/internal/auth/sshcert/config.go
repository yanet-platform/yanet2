package sshcert

import (
	"time"
)

// Config configures SSH Certificate Authentication.
type Config struct {
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
