package x509

import (
	"bytes"
	"crypto/x509"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"sync/atomic"
	"time"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/metrics"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/loader"
)

const (
	// metricCRLNextUpdate is the unix time a revocation list expects its
	// successor to be published by.
	metricCRLNextUpdate = "auth_x509_crl_next_update_seconds"
	// metricRefreshErrors counts the refreshes of a source that failed.
	metricRefreshErrors = "auth_x509_refresh_errors_total"
	// labelSource names the configured source a metric is about.
	labelSource = "source"
	// pemTypeCertificate is the PEM block type of a certificate.
	pemTypeCertificate = "CERTIFICATE"
	// pemTypeRevocationList is the PEM block type of a revocation list.
	pemTypeRevocationList = "X509 CRL"
)

// oidDeltaCRLIndicator marks a list that only holds changes against a base
// list, RFC 5280 section 5.2.4.
var oidDeltaCRLIndicator = asn1.ObjectIdentifier{2, 5, 29, 27}

// revocationKey identifies a certificate the way a revocation list does: by
// issuer name, issuer key and serial number.
//
// The issuer key keeps an old and a new authority of the same name apart
// during a rollover, when their serial sequences may overlap.
type revocationKey struct {
	Issuer string
	KeyID  string
	Serial string
}

// snapshot is one immutable view of the trust material.
type snapshot struct {
	Pool        *x509.CertPool
	Authorities []*x509.Certificate
	Lists       map[string]*x509.RevocationList
	Revoked     map[revocationKey]struct{}
}

// RevocationListInfo describes one loaded revocation list.
type RevocationListInfo struct {
	// Source is the configured source the list came from.
	Source string
	// NextUpdate is when the issuer expects to publish the next list.
	NextUpdate time.Time
}

// Store holds the authorities client certificates are verified against and
// the revocation lists they are checked against.
//
// Every read sees one consistent snapshot. A reload replaces the snapshot
// as a whole: when an authority source fails the old snapshot stays, and a
// revocation list that fails to load or verify keeps the last accepted one
// for its source, so a stalled PKI never widens what is accepted.
type Store struct {
	snapshot      atomic.Pointer[snapshot]
	caSources     []loader.Loader
	crlSources    []loader.Loader
	refreshErrors map[string]*metrics.Counter
}

// NewStore loads the authorities and revocation lists from their sources.
//
// Every source must load and every list must be signed by one of the
// authorities, so a misconfigured source fails startup instead of silently
// disabling revocation.
func NewStore(caSources []loader.Loader, crlSources []loader.Loader) (*Store, error) {
	m := &Store{
		caSources:     caSources,
		crlSources:    crlSources,
		refreshErrors: map[string]*metrics.Counter{},
	}
	for _, source := range caSources {
		m.refreshErrors[source.Source()] = &metrics.Counter{}
	}
	for _, source := range crlSources {
		m.refreshErrors[source.Source()] = &metrics.Counter{}
	}

	if err := m.Reload(); err != nil {
		return nil, err
	}

	return m, nil
}

// Pool returns the authorities of the current snapshot.
func (m *Store) Pool() *x509.CertPool {
	return m.snapshot.Load().Pool
}

// IsRevoked reports whether a loaded revocation list names the certificate.
func (m *Store) IsRevoked(certificate *x509.Certificate) bool {
	key := newRevocationKey(certificate.RawIssuer, certificate.AuthorityKeyId, certificate.SerialNumber)
	_, ok := m.snapshot.Load().Revoked[key]

	return ok
}

// RevocationLists describes the lists of the current snapshot in source
// order.
func (m *Store) RevocationLists() []RevocationListInfo {
	current := m.snapshot.Load()

	infos := make([]RevocationListInfo, 0, len(current.Lists))
	for _, source := range m.crlSources {
		list, ok := current.Lists[source.Source()]
		if !ok {
			continue
		}
		infos = append(infos, RevocationListInfo{
			Source:     source.Source(),
			NextUpdate: list.NextUpdate,
		})
	}

	return infos
}

// Reload rebuilds the snapshot from the sources.
//
// A failed authority source aborts the reload and keeps the current
// snapshot. A revocation list source that fails, or serves a list older
// than the one accepted from it, keeps that last accepted list. Every
// failure is counted and returned, joined.
func (m *Store) Reload() error {
	authorities, err := m.loadAuthorities()
	if err != nil {
		return err
	}

	pool := x509.NewCertPool()
	for _, authority := range authorities {
		pool.AddCert(authority)
	}

	var errs []error
	lists := map[string]*x509.RevocationList{}
	previous := m.snapshot.Load()
	for _, source := range m.crlSources {
		var kept *x509.RevocationList
		if previous != nil {
			kept = previous.Lists[source.Source()]
		}

		list, err := loadRevocationList(source, authorities)
		if err == nil && kept != nil && isOlder(list, kept) {
			err = ErrRevocationListRollback
		}
		if err != nil {
			m.refreshErrors[source.Source()].Inc()
			errs = append(errs, fmt.Errorf("load revocation list from %q: %w", source.Source(), err))
			if kept != nil {
				lists[source.Source()] = kept
			}
			continue
		}
		lists[source.Source()] = list
	}

	m.snapshot.Store(&snapshot{
		Pool:        pool,
		Authorities: authorities,
		Lists:       lists,
		Revoked:     indexRevoked(lists),
	})

	return errors.Join(errs...)
}

// Collect reports the next update of every loaded revocation list and the
// failed refreshes of every source.
//
// A source is labeled without the credentials and query its URL may carry,
// since metrics are read more widely than the configuration.
func (m *Store) Collect() []*commonpb.Metric {
	var out []*commonpb.Metric
	for _, info := range m.RevocationLists() {
		out = append(out, commonpb.NewMetricGauge(
			metricCRLNextUpdate,
			float64(info.NextUpdate.Unix()),
			commonpb.MetricLabelsToProto(metrics.Labels{labelSource: sourceLabel(info.Source)})...,
		))
	}
	for _, sources := range [][]loader.Loader{m.caSources, m.crlSources} {
		for _, source := range sources {
			out = append(out, commonpb.NewMetricCounter(
				metricRefreshErrors,
				m.refreshErrors[source.Source()].Load(),
				commonpb.MetricLabelsToProto(metrics.Labels{labelSource: sourceLabel(source.Source())})...,
			))
		}
	}

	return out
}

// loadAuthorities loads and merges every authority bundle.
func (m *Store) loadAuthorities() ([]*x509.Certificate, error) {
	var authorities []*x509.Certificate
	for _, source := range m.caSources {
		certificates, err := loadCertificates(source)
		if err != nil {
			m.refreshErrors[source.Source()].Inc()
			return nil, fmt.Errorf("load authorities from %q: %w", source.Source(), err)
		}
		authorities = append(authorities, certificates...)
	}

	return authorities, nil
}

// loadCertificates reads a PEM bundle from source.
func loadCertificates(source loader.Loader) ([]*x509.Certificate, error) {
	data, err := source.Load()
	if err != nil {
		return nil, err
	}

	return parseCertificates(data)
}

// parseCertificates parses every certificate block of a PEM bundle and
// requires each to be an authority.
//
// A certificate placed in the pool verifies itself as a chain of one, so an
// end-entity certificate here would authenticate itself instead of
// reporting the misconfiguration.
func parseCertificates(data []byte) ([]*x509.Certificate, error) {
	var certificates []*x509.Certificate
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			break
		}
		if block.Type != pemTypeCertificate {
			continue
		}

		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse certificate: %w", err)
		}
		if !certificate.BasicConstraintsValid || !certificate.IsCA {
			return nil, fmt.Errorf("%w: %s", ErrNotAuthority, certificate.Subject)
		}
		certificates = append(certificates, certificate)
	}

	if len(certificates) == 0 {
		return nil, ErrNoAuthorities
	}

	return certificates, nil
}

// loadRevocationList reads one list, DER or PEM, and accepts it only when
// one of the authorities signed it.
func loadRevocationList(source loader.Loader, authorities []*x509.Certificate) (*x509.RevocationList, error) {
	data, err := source.Load()
	if err != nil {
		return nil, err
	}

	if block, _ := pem.Decode(data); block != nil && block.Type == pemTypeRevocationList {
		data = block.Bytes
	}

	list, err := x509.ParseRevocationList(data)
	if err != nil {
		return nil, fmt.Errorf("parse revocation list: %w", err)
	}

	// A delta list indexed as a complete one would drop every revocation
	// of its base list.
	for _, extension := range list.Extensions {
		if extension.Id.Equal(oidDeltaCRLIndicator) {
			return nil, ErrDeltaRevocationList
		}
	}

	for _, authority := range authorities {
		if !bytes.Equal(authority.RawSubject, list.RawIssuer) {
			continue
		}
		if err := list.CheckSignatureFrom(authority); err == nil {
			return list, nil
		}
	}

	return nil, ErrUntrustedRevocationList
}

// isOlder reports whether candidate predates accepted from the same
// issuer, by list number when both carry one and by issue time otherwise.
//
// Lists of different issuers, by name or by signing key, are not ordered,
// so a rotated authority may start its numbering afresh at the same source
// even when it keeps its name.
func isOlder(candidate, accepted *x509.RevocationList) bool {
	if !bytes.Equal(candidate.RawIssuer, accepted.RawIssuer) ||
		!bytes.Equal(candidate.AuthorityKeyId, accepted.AuthorityKeyId) {
		return false
	}
	if candidate.Number != nil && accepted.Number != nil {
		return candidate.Number.Cmp(accepted.Number) < 0
	}

	return candidate.ThisUpdate.Before(accepted.ThisUpdate)
}

// sourceLabel names a source in metrics, stripping the user, query and
// fragment off a URL.
func sourceLabel(source string) string {
	parsed, err := url.Parse(source)
	if err != nil || parsed.Host == "" {
		return source
	}

	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""

	return parsed.String()
}

// indexRevoked flattens the lists into one lookup set.
func indexRevoked(lists map[string]*x509.RevocationList) map[revocationKey]struct{} {
	revoked := map[revocationKey]struct{}{}
	for _, list := range lists {
		for _, entry := range list.RevokedCertificateEntries {
			revoked[newRevocationKey(list.RawIssuer, list.AuthorityKeyId, entry.SerialNumber)] = struct{}{}
		}
	}

	return revoked
}

func newRevocationKey(rawIssuer []byte, authorityKeyID []byte, serial *big.Int) revocationKey {
	return revocationKey{
		Issuer: string(rawIssuer),
		KeyID:  string(authorityKeyID),
		Serial: serial.String(),
	}
}
