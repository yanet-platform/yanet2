package xgrpc

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/common/go/bytesource"
)

const (
	// defaultCertificateRefreshInterval is how long a key pair entry goes
	// between reloads while its pair is not about to expire.
	defaultCertificateRefreshInterval = time.Hour
	// keyPairTypeFile is the key pair type read from two PEM files.
	keyPairTypeFile = "file"
	// yamlMergeTag is the resolved tag of a YAML merge key.
	yamlMergeTag = "!!merge"
	// pemBlockStart opens every PEM block.
	pemBlockStart = "-----BEGIN"
)

var (
	// errOutsideValidity is why a pair before its start or after its
	// expiry is not presented.
	errOutsideValidity = errors.New("certificate is outside its validity period")
	// errTruncatedChain rejects a chain cut short in a later certificate,
	// which would otherwise pass as its leaf alone.
	errTruncatedChain = errors.New("certificate chain ends in a truncated PEM block")
)

// KeyPairLoader loads one certificate chain with its private key.
//
// A loader only reads: the certificate store decides when to load and
// whether to adopt what a load returns.
type KeyPairLoader interface {
	// Source names where the pair comes from in logs and metrics, without
	// any key material.
	Source() string
	// Load returns the PEM certificate chain, leaf first, and the PEM
	// private key of the leaf.
	Load() ([]byte, []byte, error)
}

// KeyPairConfig configures one key pair entry: a loader type and the
// settings of that type.
type KeyPairConfig struct {
	// Type selects the loader. Only "file" exists, reading the cert_file
	// and key_file settings.
	Type string `yaml:"type"`
	// Config holds the settings of the type, decoded strictly when the
	// loader is built.
	Config yaml.Node `yaml:"config"`
}

// Validate builds the loader without loading it, so an unknown type or
// setting fails when the config is loaded.
func (m *KeyPairConfig) Validate() error {
	_, err := NewKeyPairLoader(*m)
	return err
}

// NewKeyPairLoader builds the loader the config selects by its type.
//
// The settings are decoded strictly, so a misspelled key fails instead of
// leaving its setting empty.
func NewKeyPairLoader(config KeyPairConfig) (KeyPairLoader, error) {
	factories := map[string]func(node *yaml.Node) (KeyPairLoader, error){
		keyPairTypeFile: decodeKeyPairFiles,
	}

	factory, ok := factories[config.Type]
	if !ok {
		return nil, fmt.Errorf("unknown key pair type %q", config.Type)
	}

	return factory(&config.Config)
}

// keyPairFilesConfig is the settings of a key pair read from two files.
type keyPairFilesConfig struct {
	// CertFile is the PEM certificate chain, leaf first.
	CertFile string `yaml:"cert_file"`
	// KeyFile is the PEM private key of the leaf.
	KeyFile string `yaml:"key_file"`
}

// decodeKeyPairFiles builds a file key pair from its settings.
func decodeKeyPairFiles(node *yaml.Node) (KeyPairLoader, error) {
	var config keyPairFilesConfig
	if err := decodeStrict(node, &config); err != nil {
		return nil, fmt.Errorf("decode file key pair config: %w", err)
	}
	if config.CertFile == "" || config.KeyFile == "" {
		return nil, errors.New("cert_file and key_file are both required under config")
	}

	return NewKeyPairFiles(config.CertFile, config.KeyFile), nil
}

// decodeStrict decodes the node into out, a pointer to a struct, refusing
// mapping keys the struct does not declare.
//
// The node may be an alias, hold aliased values and merge in other
// mappings; a mapping that merges itself is an error. Errors carry the line
// numbers of the file.
func decodeStrict(node *yaml.Node, out any) error {
	node = resolveAlias(node)
	if node.Kind == 0 {
		return nil
	}

	known := yamlKeys(reflect.TypeOf(out).Elem())
	if err := checkKnownKeys(node, known, map[*yaml.Node]bool{}); err != nil {
		return err
	}

	return node.Decode(out)
}

// checkKnownKeys rejects a mapping key outside the known ones, including
// the keys merge keys pull in from other mappings.
//
// Every mapping walked is recorded as under way, then as done, so a mapping
// merged again is not walked again and one that merges itself is an error.
func checkKnownKeys(node *yaml.Node, known map[string]bool, visits map[*yaml.Node]bool) error {
	node = resolveAlias(node)
	if node.Kind != yaml.MappingNode {
		return nil
	}
	if done, ok := visits[node]; ok {
		if !done {
			return fmt.Errorf("line %d: mapping merges itself", node.Line)
		}
		return nil
	}
	visits[node] = false

	for idx := 0; idx+1 < len(node.Content); idx += 2 {
		key := resolveAlias(node.Content[idx])
		if key.ShortTag() != yamlMergeTag {
			if !known[key.Value] {
				return fmt.Errorf("line %d: unknown key %q", key.Line, key.Value)
			}
			continue
		}

		merged := resolveAlias(node.Content[idx+1])
		sources := []*yaml.Node{merged}
		if merged.Kind == yaml.SequenceNode {
			sources = merged.Content
		}
		for _, source := range sources {
			if err := checkKnownKeys(source, known, visits); err != nil {
				return err
			}
		}
	}
	visits[node] = true

	return nil
}

// resolveAlias returns the node an alias stands for, else the node itself.
func resolveAlias(node *yaml.Node) *yaml.Node {
	if node.Kind == yaml.AliasNode && node.Alias != nil {
		return node.Alias
	}

	return node
}

// yamlKeys returns the mapping keys a struct type decodes, as the yaml tags
// of its fields name them.
func yamlKeys(structType reflect.Type) map[string]bool {
	keys := map[string]bool{}
	for idx := range structType.NumField() {
		field := structType.Field(idx)
		name, _, _ := strings.Cut(field.Tag.Get("yaml"), ",")
		if !field.IsExported() || name == "-" {
			continue
		}
		if name == "" {
			name = strings.ToLower(field.Name)
		}
		keys[name] = true
	}

	return keys
}

// KeyPairFiles loads a key pair from a PEM certificate chain file and a PEM
// private key file, reading both on every load.
type KeyPairFiles struct {
	certFile string
	keyFile  string
}

// NewKeyPairFiles returns a loader reading the given files.
func NewKeyPairFiles(certFile, keyFile string) *KeyPairFiles {
	return &KeyPairFiles{
		certFile: certFile,
		keyFile:  keyFile,
	}
}

// Source names the certificate file.
func (m *KeyPairFiles) Source() string {
	return m.certFile
}

// Load reads the certificate file, then the key file.
//
// The two reads are not one atomic step, so a pair replaced file by file
// may be read half written; the store then sees a key that does not match
// and keeps its current pair.
func (m *KeyPairFiles) Load() ([]byte, []byte, error) {
	certPEM, err := bytesource.NewFileLoader(m.certFile).Load()
	if err != nil {
		return nil, nil, err
	}
	keyPEM, err := bytesource.NewFileLoader(m.keyFile).Load()
	if err != nil {
		return nil, nil, err
	}

	return certPEM, keyPEM, nil
}

type certificateStoreOptions struct {
	RefreshInterval time.Duration
	Log             *zap.Logger
}

func newCertificateStoreOptions() *certificateStoreOptions {
	return &certificateStoreOptions{
		RefreshInterval: defaultCertificateRefreshInterval,
		Log:             zap.NewNop(),
	}
}

// CertificateStoreOption configures a CertificateStore.
type CertificateStoreOption func(*certificateStoreOptions)

// WithCertificateStoreLog sets the logger for the CertificateStore.
func WithCertificateStoreLog(log *zap.Logger) CertificateStoreOption {
	return func(o *certificateStoreOptions) {
		o.Log = log
	}
}

// WithCertificateStoreRefreshInterval sets how long an entry goes between
// reloads while its pair is not about to expire.
//
// Default: 1h. Zero reloads on every use.
func WithCertificateStoreRefreshInterval(interval time.Duration) CertificateStoreOption {
	return func(o *certificateStoreOptions) {
		o.RefreshInterval = interval
	}
}

// KeyPairLeaf is the certificate one key pair entry presents.
type KeyPairLeaf struct {
	// Source names where the entry loads its pair from.
	Source string
	// Leaf is the certificate of the entry's current pair.
	Leaf *x509.Certificate
}

// keyPair is a certificate chain with its private key and parsed leaf.
type keyPair struct {
	// Certificate is what a handshake presents.
	Certificate *tls.Certificate
	// Reported is set once a handshake has logged this pair as unusable,
	// sent as no certificate or served outside its validity period, so that
	// is logged once per pair.
	Reported atomic.Bool
}

// keyPairEntry is the state of one entry, replaced whole by a refresh.
type keyPairEntry struct {
	// Source names where the entry loads its pair from.
	Source string
	// Active is the pair the entry presents.
	Active *keyPair
	// Staged is a loaded pair not yet valid, presented from its start on.
	Staged *keyPair
	// Digest identifies the content last loaded, adopted or not.
	Digest [sha256.Size]byte
	// LoadError is the failure of the last load, empty after a success.
	LoadError string
	// TriedAt is when the entry was last loaded or tried.
	TriedAt time.Time
}

// PromotionDue reports whether the staged pair has become valid.
func (m *keyPairEntry) PromotionDue(now time.Time) bool {
	return m.Staged != nil && !now.Before(m.Staged.Certificate.Leaf.NotBefore)
}

// ReloadDue reports whether the entry loads again on this use.
//
// It does once the interval has passed since its last try, and on every
// use while its pair expires within one interval or already has.
func (m *keyPairEntry) ReloadDue(now time.Time, interval time.Duration) bool {
	if now.Sub(m.TriedAt) >= interval {
		return true
	}

	return !now.Add(interval).Before(m.Active.Certificate.Leaf.NotAfter)
}

// certificateSnapshot is one immutable view of every entry, in
// configuration order.
type certificateSnapshot struct {
	Entries []keyPairEntry
}

// Due reports whether any entry has a reload or a promotion due.
func (m *certificateSnapshot) Due(now time.Time, interval time.Duration) bool {
	for idx := range m.Entries {
		if m.Entries[idx].PromotionDue(now) || m.Entries[idx].ReloadDue(now, interval) {
			return true
		}
	}

	return false
}

// CertificateStore presents the certificates of a TLS endpoint from key
// pair entries that rotate without a restart.
//
// Every read sees one snapshot of all entries. An entry reloads on use
// once the refresh interval has passed since its last try, and on every
// use while its pair expires within one interval or already has. A loaded
// pair replaces the entry's pair only when it parses, matches its key and
// is valid now; a pair not yet valid waits and takes over at its start,
// and anything else leaves the entry as it was. One caller refreshes at a
// time while the others serve the snapshot as it stands, and nothing runs
// in the background.
type CertificateStore struct {
	snapshot        atomic.Pointer[certificateSnapshot]
	loaders         []KeyPairLoader
	refreshInterval time.Duration
	refreshMu       sync.Mutex
	log             *zap.Logger
}

// NewCertificateStore loads every entry and returns the store presenting
// them.
//
// Every entry must load, parse and match its key, so a misconfigured pair
// fails startup. A pair outside its validity period still loads, with a
// warning.
func NewCertificateStore(loaders []KeyPairLoader, options ...CertificateStoreOption) (*CertificateStore, error) {
	opts := newCertificateStoreOptions()
	for _, o := range options {
		o(opts)
	}

	if len(loaders) == 0 {
		return nil, errors.New("no key pairs to load")
	}

	m := &CertificateStore{
		loaders:         loaders,
		refreshInterval: opts.RefreshInterval,
		log:             opts.Log,
	}

	now := time.Now()
	entries := make([]keyPairEntry, 0, len(loaders))
	for _, loader := range loaders {
		entry, err := m.load(loader, now)
		if err != nil {
			return nil, fmt.Errorf("load key pair from %q: %w", loader.Source(), err)
		}
		entries = append(entries, entry)
	}
	m.snapshot.Store(&certificateSnapshot{Entries: entries})

	return m, nil
}

// GetCertificate serves a handshake the first entry whose pair is valid
// now and supported by the client hello, else the first entry, as Go
// selects over a static certificate list.
//
// A first entry served outside its validity period is logged once per pair.
func (m *CertificateStore) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	now := time.Now()
	snapshot := m.current(now)
	if certificate := selectCertificate(snapshot.Entries, now, hello.SupportsCertificate); certificate != nil {
		return certificate, nil
	}

	first := snapshot.Entries[0]
	if !validAt(first.Active.Certificate.Leaf, now) && first.Active.Reported.CompareAndSwap(false, true) {
		m.log.Warn("no key pair is valid and supported by the client, serving the first one outside its validity period",
			keyPairFields(first.Source, first.Active.Certificate.Leaf)...,
		)
	}

	return first.Active.Certificate, nil
}

// GetClientCertificate presents the first entry whose pair is valid now
// and accepted by the server's request, else no certificate, as Go selects
// over a static certificate list.
//
// A pair outside its validity period is never sent, so the connection goes
// on without a certificate instead of failing its handshake.
func (m *CertificateStore) GetClientCertificate(request *tls.CertificateRequestInfo) (*tls.Certificate, error) {
	now := time.Now()
	snapshot := m.current(now)
	if certificate := selectCertificate(snapshot.Entries, now, request.SupportsCertificate); certificate != nil {
		return certificate, nil
	}

	for _, entry := range snapshot.Entries {
		if !entry.Active.Reported.CompareAndSwap(false, true) {
			continue
		}

		reason := errOutsideValidity
		if validAt(entry.Active.Certificate.Leaf, now) {
			reason = request.SupportsCertificate(entry.Active.Certificate)
		}
		m.log.Warn("no key pair is valid and accepted by the server, sending no client certificate",
			append(keyPairFields(entry.Source, entry.Active.Certificate.Leaf), zap.NamedError("reason", reason))...,
		)
	}

	return &tls.Certificate{}, nil
}

// Leaves returns the certificate each entry presents, in configuration
// order, after refreshing the entries that are due.
func (m *CertificateStore) Leaves() []KeyPairLeaf {
	snapshot := m.current(time.Now())

	leaves := make([]KeyPairLeaf, 0, len(snapshot.Entries))
	for _, entry := range snapshot.Entries {
		leaves = append(leaves, KeyPairLeaf{
			Source: entry.Source,
			Leaf:   entry.Active.Certificate.Leaf,
		})
	}

	return leaves
}

// current returns the snapshot to serve now, first reloading the entries
// that are due and promoting staged pairs that became valid.
//
// A caller that finds a refresh under way serves the snapshot as it stands
// instead of waiting for it.
func (m *CertificateStore) current(now time.Time) *certificateSnapshot {
	snapshot := m.snapshot.Load()
	if !snapshot.Due(now, m.refreshInterval) || !m.refreshMu.TryLock() {
		return snapshot
	}
	defer m.refreshMu.Unlock()

	// A refresh may have completed between the first look and the lock.
	snapshot = m.snapshot.Load()
	if !snapshot.Due(now, m.refreshInterval) {
		return snapshot
	}

	entries := make([]keyPairEntry, 0, len(snapshot.Entries))
	for idx, entry := range snapshot.Entries {
		if entry.PromotionDue(now) {
			entry = m.promote(entry, now)
		}
		if entry.ReloadDue(now, m.refreshInterval) {
			entry = m.reload(m.loaders[idx], entry, now)
		}
		entries = append(entries, entry)
	}

	next := &certificateSnapshot{Entries: entries}
	m.snapshot.Store(next)

	return next
}

// load reads the first pair of an entry, adopting it whatever its validity.
func (m *CertificateStore) load(loader KeyPairLoader, now time.Time) (keyPairEntry, error) {
	certPEM, keyPEM, err := loader.Load()
	if err != nil {
		return keyPairEntry{}, err
	}
	certificate, err := parseKeyPair(certPEM, keyPEM)
	if err != nil {
		return keyPairEntry{}, err
	}

	fields := keyPairFields(loader.Source(), certificate.Leaf)
	if validAt(certificate.Leaf, now) {
		m.log.Info("loaded key pair", fields...)
	} else {
		m.log.Warn("loaded key pair outside its validity period", fields...)
	}

	return keyPairEntry{
		Source:  loader.Source(),
		Active:  &keyPair{Certificate: certificate},
		Digest:  contentDigest(certPEM, keyPEM),
		TriedAt: now,
	}, nil
}

// reload loads the entry again and adopts, stages or rejects the pair.
//
// A failed load or a rejected pair keeps the entry's pairs. A failure is
// reported unless the previous load failed the same way, and a content
// only when it differs from the one loaded last.
func (m *CertificateStore) reload(loader KeyPairLoader, entry keyPairEntry, now time.Time) keyPairEntry {
	entry.TriedAt = now

	certPEM, keyPEM, err := loader.Load()
	if err != nil {
		if err.Error() != entry.LoadError {
			m.log.Warn("failed to load key pair, keeping the current one",
				zap.String("source", entry.Source),
				zap.Error(err),
			)
		}
		entry.LoadError = err.Error()
		return entry
	}
	entry.LoadError = ""

	digest := contentDigest(certPEM, keyPEM)
	if digest == entry.Digest {
		return entry
	}
	entry.Digest = digest

	certificate, err := parseKeyPair(certPEM, keyPEM)
	if err != nil {
		m.log.Warn("rejected key pair, keeping the current one",
			zap.String("source", entry.Source),
			zap.Error(err),
		)
		return entry
	}

	leaf := certificate.Leaf
	fields := keyPairFields(entry.Source, leaf)
	switch {
	case now.Before(leaf.NotBefore):
		entry.Staged = &keyPair{Certificate: certificate}
		m.log.Info("staged key pair until it becomes valid", fields...)
	case now.After(leaf.NotAfter):
		m.log.Warn("rejected expired key pair, keeping the current one", fields...)
	default:
		entry.Active = &keyPair{Certificate: certificate}
		entry.Staged = nil
		m.log.Info("adopted key pair", fields...)
	}

	return entry
}

// promote makes the staged pair of an entry current once it is valid, or
// drops it when it expired before any use could present it.
func (m *CertificateStore) promote(entry keyPairEntry, now time.Time) keyPairEntry {
	staged := entry.Staged
	entry.Staged = nil

	fields := keyPairFields(entry.Source, staged.Certificate.Leaf)
	if now.After(staged.Certificate.Leaf.NotAfter) {
		m.log.Warn("dropped staged key pair that expired before it was presented", fields...)
		return entry
	}

	entry.Active = staged
	m.log.Info("adopted staged key pair", fields...)

	return entry
}

// selectCertificate returns the first pair among the entries that is valid
// now and that the peer supports, nil when there is none.
func selectCertificate(
	entries []keyPairEntry,
	now time.Time,
	supports func(certificate *tls.Certificate) error,
) *tls.Certificate {
	for _, entry := range entries {
		certificate := entry.Active.Certificate
		if validAt(certificate.Leaf, now) && supports(certificate) == nil {
			return certificate
		}
	}

	return nil
}

// parseKeyPair parses a PEM certificate chain and the PEM private key of
// its leaf, failing when the key does not match the leaf.
func parseKeyPair(certPEM, keyPEM []byte) (*tls.Certificate, error) {
	certificate, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	if hasTruncatedBlock(certPEM) {
		return nil, errTruncatedChain
	}

	// Selection reads the leaf on every handshake, and a compatibility
	// setting of the standard library leaves it unparsed.
	if certificate.Leaf == nil {
		certificate.Leaf, err = x509.ParseCertificate(certificate.Certificate[0])
		if err != nil {
			return nil, err
		}
	}

	return &certificate, nil
}

// hasTruncatedBlock reports whether a PEM block starts after the last one
// that decodes; other text there, such as a comment, is allowed.
func hasTruncatedBlock(data []byte) bool {
	rest := data
	for {
		block, next := pem.Decode(rest)
		if block == nil {
			return bytes.Contains(rest, []byte(pemBlockStart))
		}
		rest = next
	}
}

// contentDigest identifies a loaded chain and key without keeping the key.
func contentDigest(certPEM, keyPEM []byte) [sha256.Size]byte {
	hash := sha256.New()
	_, _ = hash.Write(binary.BigEndian.AppendUint64(nil, uint64(len(certPEM))))
	_, _ = hash.Write(certPEM)
	_, _ = hash.Write(keyPEM)

	var digest [sha256.Size]byte
	hash.Sum(digest[:0])

	return digest
}

// validAt reports whether now falls within the certificate's validity
// period.
func validAt(leaf *x509.Certificate, now time.Time) bool {
	return !now.Before(leaf.NotBefore) && !now.After(leaf.NotAfter)
}

// keyPairFields describes a pair in the log by its source and certificate
// metadata, never by its key.
//
// The serial is upper-case hex, as openssl prints it.
func keyPairFields(source string, leaf *x509.Certificate) []zap.Field {
	return []zap.Field{
		zap.String("source", source),
		zap.String("subject", leaf.Subject.String()),
		zap.String("issuer", leaf.Issuer.String()),
		zap.String("serial", strings.ToUpper(hex.EncodeToString(leaf.SerialNumber.Bytes()))),
		zap.Time("not_before", leaf.NotBefore),
		zap.Time("not_after", leaf.NotAfter),
	}
}
