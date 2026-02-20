package sshkey

import (
	"time"
)

// Config configures SSH Key Authentication.
type Config struct {
	// KeysPath is the path to the ssh_keys.yaml file.
	KeysPath string `yaml:"keys_path"`
	// TimeWindow is the timestamp tolerance window for replay protection.
	// Tokens with timestamps outside this window are rejected.
	//
	// Default: 5s.
	TimeWindow time.Duration `yaml:"time_window"`
}
