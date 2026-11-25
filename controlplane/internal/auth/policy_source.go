package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sys/unix"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/identity"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/permission"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/rbac"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/sshcert"
)

const (
	defaultPolicyRefresh  = 2 * time.Second
	policyStartupLockWait = time.Second
	policyStartupRetry    = 20 * time.Millisecond
	maxPolicyManifestSize = 4 * 1024
	maxPolicyFileSize     = 2 * 1024 * 1024
)

// PolicyScope binds a publication to one server role on a specific node.
type PolicyScope struct {
	Epoch     string `json:"epoch" yaml:"epoch"`
	NodeUID   string `json:"node_uid" yaml:"node_uid"`
	ServerURI string `json:"server_uri" yaml:"server_uri"`
}

// ManagedPolicyConfig configures the lifetime and scope of a local reader.
type ManagedPolicyConfig struct {
	Directory       string        `yaml:"directory"`
	ExpectedScope   PolicyScope   `yaml:"expected_scope"`
	RefreshInterval time.Duration `yaml:"refresh_interval,omitempty"`
	SSHTrust        bool          `yaml:"ssh_trust,omitempty"`
}

// Validate rejects an unbound source and invalid poll intervals.
func (m *ManagedPolicyConfig) Validate() error {
	if !filepath.IsAbs(m.Directory) || filepath.Clean(m.Directory) != m.Directory || m.Directory == "/" {
		return fmt.Errorf("directory must be a clean absolute path to the policy store")
	}
	if m.ExpectedScope.Epoch == "" || m.ExpectedScope.NodeUID == "" || !strings.HasPrefix(m.ExpectedScope.ServerURI, "spiffe://") {
		return fmt.Errorf("expected_scope requires epoch, node_uid and the server URI")
	}
	if m.RefreshInterval < 0 {
		return fmt.Errorf("refresh_interval must not be negative")
	}
	return nil
}

type policyManifest struct {
	Version  int         `json:"version"`
	Scope    PolicyScope `json:"scope"`
	Sequence uint64      `json:"sequence"`
	Identity string      `json:"identities_sha256"`
	Grants   string      `json:"permissions_sha256"`
	SSHTrust string      `json:"ssh_sha256,omitempty"`
}

type policySnapshot struct {
	Sequence         uint64
	Generation       string
	Activated        time.Time
	IdentityProvider identity.Provider
	Authorizer       Authorizer
	SSHTrust         *sshcert.CAStore
}

// Revision is zero when authorization is not managed by the publisher.
func (m *policySnapshot) Revision() uint64 {
	if m == nil {
		return 0
	}
	return m.Sequence
}

// Authorize checks a grant unless authorization is disabled.
func (m *policySnapshot) Authorize(ctx context.Context, principal *core.Principal, method string) error {
	if m == nil {
		return nil
	}
	return m.Authorizer.Authorize(ctx, principal, method)
}

type policyBytes struct {
	Sequence   uint64
	Generation string
	Identities []byte
	Grants     []byte
	SSHTrust   []byte
}

func readPolicy(config ManagedPolicyConfig, sequence uint64, generation string) (*policySnapshot, error) {
	publication, err := readPolicyGeneration(config, sequence, generation)
	if err != nil || publication == nil {
		return nil, err
	}
	identities, err := identity.ParsePolicyIdentities(publication.Identities)
	if err != nil {
		return nil, fmt.Errorf("parse managed identities: %w", err)
	}
	permissions, err := permission.ParsePolicyPermissions(publication.Grants)
	if err != nil {
		return nil, fmt.Errorf("compile managed permissions: %w", err)
	}
	var trust *sshcert.CAStore
	if config.SSHTrust {
		trust, err = sshcert.ParsePolicyCA(publication.SSHTrust)
		if err != nil {
			return nil, fmt.Errorf("parse managed SSH trust: %w", err)
		}
	}
	return &policySnapshot{
		Sequence:         publication.Sequence,
		Generation:       publication.Generation,
		IdentityProvider: identities,
		// Stream revocation calls the authorizer while holding the admission
		// lock. Request admission logs denials separately.
		Authorizer: rbac.NewRBACAuthorizer(permissions),
		SSHTrust:   trust,
	}, nil
}

var errPolicyWriterBusy = errors.New("policy writer holds the publication lock")

func readInitialPolicy(config ManagedPolicyConfig) (*policySnapshot, error) {
	until := time.Now().Add(policyStartupLockWait)
	for {
		policy, err := readPolicy(config, 0, "")
		if !errors.Is(err, errPolicyWriterBusy) || !time.Now().Before(until) {
			return policy, err
		}
		time.Sleep(policyStartupRetry)
	}
}

func readPolicyGeneration(
	config ManagedPolicyConfig, previousSequence uint64, previousGeneration string,
) (*policyBytes, error) {
	root, err := os.OpenRoot(config.Directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	lock, err := root.OpenFile(".publish-lock", os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("open publication lock: %w", err)
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_SH|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, errPolicyWriterBusy
		}
		return nil, fmt.Errorf("lock policy publication: %w", err)
	}

	generation, err := root.Readlink("current")
	if err != nil {
		return nil, fmt.Errorf("read current policy generation: %w", err)
	}
	if generation != "" && generation == previousGeneration {
		return nil, nil
	}
	if digest, err := hex.DecodeString(generation); err != nil || len(digest) != sha256.Size {
		return nil, fmt.Errorf("invalid policy generation name")
	}
	info, err := root.Lstat(generation)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("policy generation is not a real directory")
	}
	pinned, err := root.OpenRoot(generation)
	if err != nil {
		return nil, err
	}
	defer pinned.Close()

	data, err := readPolicyFile(pinned, "policy.json", generation, maxPolicyManifestSize)
	if err != nil {
		return nil, err
	}
	var manifest policyManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("decode policy manifest: %w", err)
	}
	canonical, err := json.Marshal(manifest)
	if err != nil || !bytes.Equal(data, canonical) {
		return nil, fmt.Errorf("policy manifest must be canonical")
	}
	if manifest.Version != 1 || manifest.Scope != config.ExpectedScope || manifest.Sequence == 0 {
		return nil, fmt.Errorf("unexpected policy version, scope or revision")
	}
	if manifest.Sequence <= previousSequence {
		return nil, fmt.Errorf("policy revision %d cannot replace active revision %d", manifest.Sequence, previousSequence)
	}
	identities, err := readPolicyFile(pinned, "identities.yaml", manifest.Identity, maxPolicyFileSize)
	if err != nil {
		return nil, err
	}
	grants, err := readPolicyFile(pinned, "permissions.yaml", manifest.Grants, maxPolicyFileSize)
	if err != nil {
		return nil, err
	}
	var trust []byte
	if manifest.SSHTrust != "" {
		if !config.SSHTrust {
			return nil, fmt.Errorf("policy contains SSH trust but managed SSH authentication is not configured")
		}
		trust, err = readPolicyFile(pinned, "ssh-ca.pub", manifest.SSHTrust, maxPolicyFileSize)
		if err != nil {
			return nil, err
		}
	} else if _, err := pinned.Lstat("ssh-ca.pub"); !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("unexpected SSH trust file in policy generation")
	}

	// A rename may be visible before its writer confirms durability.
	// Sync the parent under the shared lock before using its new target.
	directory, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return nil, fmt.Errorf("confirm policy generation durability: %w", err)
	}
	return &policyBytes{manifest.Sequence, generation, identities, grants, trust}, nil
}

func readPolicyFile(root *os.Root, name, digest string, maximum int64) ([]byte, error) {
	file, err := root.OpenFile(name, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", name, err)
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !stat.Mode().IsRegular() || stat.Size() > maximum {
		return nil, fmt.Errorf("invalid policy file type or size for %s", name)
	}
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	if int64(len(data)) > maximum {
		return nil, fmt.Errorf("policy file %s exceeds size limit", name)
	}
	if policyDigest(data) != digest {
		return nil, fmt.Errorf("policy hash mismatch for %s", name)
	}
	return data, nil
}

func policyDigest(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

type policyReadResult struct {
	Policy *policySnapshot
	Err    error
}

func readPolicyAsync(
	result chan<- policyReadResult, config ManagedPolicyConfig, sequence uint64, generation string,
) {
	policy, err := readPolicy(config, sequence, generation)
	result <- policyReadResult{Policy: policy, Err: err}
}

// RunPolicyReload watches the persistent source until its gateway stops.
func (m *Manager) RunPolicyReload(ctx context.Context) error {
	if m.managedPolicy == nil {
		return nil
	}
	m.logPolicyActivation(m.active.Load())
	interval := m.managedPolicy.RefreshInterval
	if interval == 0 {
		interval = defaultPolicyRefresh
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var previousError string
	var pending <-chan policyReadResult
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if pending != nil {
				continue
			}
			// Disk reads and fsync cannot be interrupted. Allow at most one
			// in-flight read with no reference to the gateway or its policy.
			result := make(chan policyReadResult, 1)
			active := m.active.Load()
			go readPolicyAsync(result, *m.managedPolicy, active.Sequence, active.Generation)
			pending = result
		case result := <-pending:
			pending = nil
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(result.Err, errPolicyWriterBusy) {
				continue
			}
			if err := result.Err; err != nil {
				m.policyReloadErrors.Add(1)
				if message := err.Error(); message != previousError {
					m.log.Warn("gateway policy reload failed", zap.Error(err))
					previousError = message
				}
				continue
			}
			previousError = ""
			if result.Policy != nil && m.publishPolicy(result.Policy) {
				m.logPolicyActivation(result.Policy)
			}
		}
	}
}

func (m *Manager) collectPolicy() []*commonpb.Metric {
	if m.managedPolicy == nil {
		return nil
	}
	current := m.active.Load()
	return []*commonpb.Metric{
		commonpb.NewMetricGauge("auth_policy_active_revision", float64(current.Sequence)),
		commonpb.NewMetricGauge("auth_policy_activated_timestamp_seconds", float64(current.Activated.Unix())),
		commonpb.NewMetricCounter("auth_policy_reload_errors_total", m.policyReloadErrors.Load()),
	}
}

func (m *Manager) logPolicyActivation(policy *policySnapshot) {
	m.log.Info("activated gateway policy",
		zap.Uint64("sequence", policy.Sequence),
		zap.String("generation", policy.Generation),
		zap.String("server_uri", m.managedPolicy.ExpectedScope.ServerURI),
	)
}
