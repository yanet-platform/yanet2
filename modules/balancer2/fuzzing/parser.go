// This file implements the corpus-specific keepalived-style services parser
// used by the balancer2 fuzzing application. The parser is intentionally
// narrow: it understands only the constructs that appear in the in-repo
// corpora (taxi.services.conf, appbalancer.services.conf) plus a small
// envelope of related directives. Anything outside that envelope is either
// safely skipped or rejected with a line-aware error.
//
// The parser is consumed by Task 3 (state model) and Task 6 (initial
// UpdateConfig construction), so the exported surface is kept stable: a
// Corpus aggregates VirtualServer entries; each VirtualServer carries
// scheduler, transport proto, and a stable-ordered set of RealServer
// children. Helpers convert the corpus into balancerpb VS shapes without
// pulling in controlplane internals.

package fuzzing

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/yanet-platform/yanet2/common/filterpb"
	"github.com/yanet-platform/yanet2/modules/balancer2/controlplane/balancerpb"
)

// VsKey is the immutable identity of a virtual server: address bytes,
// transport port, and protocol. It is comparable so it can be used as a
// map key in the parser, the expected-state model, and downstream tests.
type VsKey struct {
	// IP holds the canonicalised 16-byte form of an IPv4 or IPv6 address;
	// callers that need the original textual form should use
	// VirtualServer.AddrText.
	IP    [16]byte
	Port  uint16
	Proto balancerpb.TransportProto
}

// RealKey is the immutable identity of a real server within a VS.
type RealKey struct {
	IP   [16]byte
	Port uint16
}

// RealServer is a parsed real_server entry. Weight defaults to 1 when the
// corpus omits it; the parser still records the original line in Line so
// downstream errors can point at the source.
type RealServer struct {
	Key      RealKey
	AddrText string
	Weight   uint32
	Line     int
	// Bindto and BindtoMask are the source address and mask parsed from the
	// "bindto" directive inside the health-check block (HTTP_GET/SSL_GET).
	// They use the canonical 4-byte (IPv4) or 16-byte (IPv6) form so that
	// filterpb.IPNet construction is family-consistent. Both are nil when
	// the corpus omits the directive.
	Bindto     []byte
	BindtoMask []byte
}

// VirtualServer is a parsed virtual_server entry. Reals preserves the
// original corpus order; realsByKey enables O(1) duplicate detection
// inside the parser without exposing internal state.
type VirtualServer struct {
	Key        VsKey
	AddrText   string
	Scheduler  balancerpb.VsScheduler
	Reals      []*RealServer
	realsByKey map[RealKey]struct{}
	Source     string
	Line       int
}

// Corpus is the aggregate parsed view of one or more services config
// files. VSs preserves the order in which virtual servers were discovered
// across the inputs; vsByKey supports duplicate detection across files.
type Corpus struct {
	VSs     []*VirtualServer
	vsByKey map[VsKey]*VirtualServer
}

// ParseServicesCorpus reads and parses every path in order, returning a
// merged corpus. It is the production entry point used by the fuzzing
// runner during startup.
func ParseServicesCorpus(path string) (*Corpus, error) {
	corpus := newCorpus()
	if err := corpus.appendFile(path); err != nil {
		return nil, err
	}

	if len(corpus.VSs) == 0 {
		return nil, fmt.Errorf("parse services corpus: no virtual_server blocks found in %v", path)
	}

	return corpus, nil
}

// ParseServicesCorpusFromReader parses a single source identified by name.
// It exists so tests can feed in-memory fixtures without touching disk.
func ParseServicesCorpusFromReader(name string, r io.Reader) (*Corpus, error) {
	corpus := newCorpus()
	if err := corpus.appendReader(name, r); err != nil {
		return nil, err
	}
	if len(corpus.VSs) == 0 {
		return nil, fmt.Errorf("parse services corpus %q: no virtual_server blocks found", name)
	}
	return corpus, nil
}

// Lookup returns the virtual server with the given key, or nil if absent.
func (m *Corpus) Lookup(key VsKey) *VirtualServer {
	return m.vsByKey[key]
}

// ToVsConfigList converts the corpus into a balancerpb VsConfigList using
// each VS's parsed identity, scheduler, and real set. The resulting list
// is ordered identically to Corpus.VSs.
func (m *Corpus) ToVsConfigList() *balancerpb.VsConfigList {
	vsList := &balancerpb.VsConfigList{Vs: make([]*balancerpb.VsConfig, 0, len(m.VSs))}
	for _, vs := range m.VSs {
		vsList.Vs = append(vsList.Vs, vs.ToVsConfig())
	}
	return vsList
}

// ToVsConfig converts a single virtual server into a balancerpb VsConfig.
// Allowed sources, flags, and peers are left zero-valued; downstream
// model code is responsible for filling those in.
func (m *VirtualServer) ToVsConfig() *balancerpb.VsConfig {
	reals := make([]*balancerpb.RealConfig, 0, len(m.Reals))
	for _, real := range m.Reals {
		weight := real.Weight
		rc := &balancerpb.RealConfig{
			Id: &balancerpb.RelativeRealIdentifier{
				Ip:   keyIPBytes(real.Key.IP),
				Port: uint32(real.Key.Port),
			},
			Weight: &weight,
		}
		rc.Src = sourceForReal(real)
		reals = append(reals, rc)
	}
	return &balancerpb.VsConfig{
		Id: &balancerpb.VsIdentifier{
			Addr:  keyIPBytes(m.Key.IP),
			Port:  uint32(m.Key.Port),
			Proto: m.Key.Proto,
		},
		Scheduler: m.Scheduler,
		Reals:     reals,
	}
}

func newCorpus() *Corpus {
	return &Corpus{vsByKey: map[VsKey]*VirtualServer{}}
}

// appendFile opens path and delegates parsing to appendReader, ensuring
// the file handle is closed regardless of error.
func (m *Corpus) appendFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("parse services corpus %q: %w", path, err)
	}
	defer f.Close()

	return m.appendReader(path, f)
}

func (m *Corpus) appendReader(source string, r io.Reader) error {
	p := &parser{source: source, corpus: m}
	return p.parse(r)
}

// parser is the per-source state machine. It is intentionally not exported
// since the public API is the Parse* free functions.
type parser struct {
	source  string
	corpus  *Corpus
	lineNo  int
	currVS  *VirtualServer
	currRS  *RealServer
	skipper int // depth of unknown-block nesting; 0 means "not skipping".
}

// parse drives the line-by-line scan. Each physical line is split into
// one or more logical statements at brace boundaries so that constructs
// like "real_server 10.0.0.2 80 { weight 1 }" — which the in-repo
// corpora prefer for compact fixtures — are tokenised the same as their
// multi-line expansion.
func (m *parser) parse(r io.Reader) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		m.lineNo++
		line := stripComment(scanner.Text())
		tokens := tokenize(line)
		for _, stmt := range splitStatements(tokens) {
			if err := m.consume(stmt); err != nil {
				return err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("parse services corpus %q: read error at line %d: %w",
			m.source, m.lineNo, err)
	}

	if m.skipper != 0 || m.currRS != nil || m.currVS != nil {
		return fmt.Errorf("parse services corpus %q: unexpected end of input with unclosed brace",
			m.source)
	}
	return nil
}

// consume dispatches a single logical statement to the correct handler
// based on current state. Each handler is responsible for emitting
// line-aware errors.
func (m *parser) consume(tokens []string) error {
	if len(tokens) == 0 {
		return nil
	}
	if m.skipper > 0 {
		return m.consumeInSkip(tokens)
	}
	switch {
	case m.currRS != nil:
		return m.consumeInReal(tokens)
	case m.currVS != nil:
		return m.consumeInVS(tokens)
	default:
		return m.consumeAtTop(tokens)
	}
}

// consumeAtTop handles only the virtual_server opener; everything else at
// the top level is ignored to tolerate stray includes/comments.
func (m *parser) consumeAtTop(tokens []string) error {
	if tokens[0] != "virtual_server" {
		// A stray "}" at the top level signals an unbalanced brace in
		// the corpus, which is worth reporting eagerly.
		if tokens[0] == "}" {
			return m.errf("unexpected '}' at top level")
		}
		return nil
	}
	if len(tokens) < 4 || tokens[len(tokens)-1] != "{" {
		return m.errf("malformed virtual_server: expected 'virtual_server <ip> <port> {'")
	}

	ip, err := m.parseIP(tokens[1])
	if err != nil {
		return err
	}
	port, err := m.parsePort(tokens[2])
	if err != nil {
		return err
	}

	vs := &VirtualServer{
		Key: VsKey{
			IP:   ip,
			Port: port,
			// Default proto matches the on-the-wire enum default; a
			// later "protocol" directive may overwrite it.
			Proto: balancerpb.TransportProto_TCP,
		},
		AddrText:   tokens[1],
		Scheduler:  balancerpb.VsScheduler_WRR,
		realsByKey: map[RealKey]struct{}{},
		Source:     m.source,
		Line:       m.lineNo,
	}
	m.currVS = vs
	return nil
}

// consumeInVS handles directives between a "virtual_server {" and its
// matching "}". Unknown "IDENT {" lines start a skip block (used for
// virtualhost-style nested directives that we do not interpret).
func (m *parser) consumeInVS(tokens []string) error {
	switch tokens[0] {
	case "}":
		return m.closeVS()
	case "protocol":
		return m.handleProtocol(tokens)
	case "lvs_sched":
		return m.handleScheduler(tokens)
	case "real_server":
		return m.openReal(tokens)
	default:
		if tokens[len(tokens)-1] == "{" {
			m.skipper = 1
		}
		return nil
	}
}

// consumeInReal handles directives inside a real_server block. weight is
// the only directive we interpret; "IDENT {" opens a skip block
// (HTTP_GET, SSL_GET, etc.).
func (m *parser) consumeInReal(tokens []string) error {
	switch tokens[0] {
	case "}":
		return m.closeReal()
	case "weight":
		return m.handleWeight(tokens)
	default:
		if tokens[len(tokens)-1] == "{" {
			m.skipper = 1
		}
		return nil
	}
}

// consumeInSkip tracks brace depth while inside an uninterpreted nested
// block (HTTP_GET, SSL_GET, etc.). splitStatements guarantees that '{'
// and '}' arrive as their own single-token statements, so a "FOO {"
// statement increases depth and the matching "}" pops it.
//
// While at depth 1 (directly inside the first skipped block), the
// "bindto" directive is captured because it carries the tunnel source
// address needed for RealConfig.Src.
func (m *parser) consumeInSkip(tokens []string) error {
	last := tokens[len(tokens)-1]
	switch last {
	case "{":
		m.skipper++
	case "}":
		m.skipper--
		if m.skipper < 0 {
			return m.errf("unbalanced '}' inside skipped block")
		}
	}
	// Capture the first "bindto IP" seen at depth 1. After the switch
	// above, m.skipper reflects the current depth: if it is still 1
	// (neither a "{" nor a "}" changed it away from 1), and the
	// directive is "bindto", record the tunnel source on the current real.
	if m.skipper == 1 && len(tokens) == 2 && tokens[0] == "bindto" &&
		m.currRS != nil && m.currRS.Bindto == nil {
		if bindto, mask, ok := parseBindto(tokens[1]); ok {
			// Keep bindto only when it matches the real destination family.
			// Some corpora include mixed-family bindto directives that are
			// acceptable in source format but rejected by balancer config build.
			if sameAddrFamily(bindto, m.currRS.Key.IP) {
				m.currRS.Bindto = bindto
				m.currRS.BindtoMask = mask
			}
		}
	}
	return nil
}

// parseBindto converts a keepalived "bindto" IP literal into the canonical
// addr/mask byte slices used by filterpb.IPNet. IPv4 addresses are returned
// as 4-byte slices with a /32 mask; IPv6 addresses as 16-byte slices with a
// /128 mask. Returns (nil, nil, false) when the literal is invalid.
func parseBindto(token string) (ip, mask []byte, ok bool) {
	parsed := net.ParseIP(token)
	if parsed == nil {
		return nil, nil, false
	}
	if v4 := parsed.To4(); v4 != nil {
		return []byte(v4), net.IPMask{0xff, 0xff, 0xff, 0xff}, true
	}
	v6 := parsed.To16()
	fullMask := make(net.IPMask, 16)
	for i := range fullMask {
		fullMask[i] = 0xff
	}
	return []byte(v6), []byte(fullMask), true
}

func sameAddrFamily(addr []byte, key [16]byte) bool {
	return (len(addr) == net.IPv4len) == (net.IP(key[:]).To4() != nil)
}

func sourceForReal(real *RealServer) *filterpb.IPNet {
	if real.Bindto != nil && sameAddrFamily(real.Bindto, real.Key.IP) {
		return &filterpb.IPNet{
			Addr: append([]byte(nil), real.Bindto...),
			Mask: append([]byte(nil), real.BindtoMask...),
		}
	}
	// Balancer backend requires source to be set for every real; when corpus
	// bindto is absent or mixed-family, use host-network source by real family.
	if v4 := net.IP(real.Key.IP[:]).To4(); v4 != nil {
		return &filterpb.IPNet{
			Addr: append([]byte(nil), v4...),
			Mask: []byte{0xff, 0xff, 0xff, 0xff},
		}
	}
	mask := make([]byte, net.IPv6len)
	for idx := range mask {
		mask[idx] = 0xff
	}
	return &filterpb.IPNet{
		Addr: keyIPBytes(real.Key.IP),
		Mask: mask,
	}
}

// keyIPBytes returns a canonical protobuf address encoding from a parsed key:
// IPv4 addresses are emitted as 4 bytes, IPv6 as 16 bytes.
func keyIPBytes(key [16]byte) []byte {
	if v4 := net.IP(key[:]).To4(); v4 != nil {
		return append([]byte(nil), v4...)
	}
	return append([]byte(nil), key[:]...)
}

func (m *parser) handleProtocol(tokens []string) error {
	if len(tokens) != 2 {
		return m.errf("protocol: expected 'protocol TCP|UDP'")
	}
	switch strings.ToUpper(tokens[1]) {
	case "TCP":
		m.currVS.Key.Proto = balancerpb.TransportProto_TCP
	case "UDP":
		m.currVS.Key.Proto = balancerpb.TransportProto_UDP
	default:
		return m.errf("protocol: unsupported value %q (expected TCP or UDP)", tokens[1])
	}
	return nil
}

func (m *parser) handleScheduler(tokens []string) error {
	if len(tokens) != 2 {
		return m.errf("lvs_sched: expected 'lvs_sched wrr|wlc|sh|op'")
	}
	switch strings.ToLower(tokens[1]) {
	case "wrr":
		m.currVS.Scheduler = balancerpb.VsScheduler_WRR
	case "wlc":
		m.currVS.Scheduler = balancerpb.VsScheduler_WLC
	case "sh":
		m.currVS.Scheduler = balancerpb.VsScheduler_SH
	case "op":
		m.currVS.Scheduler = balancerpb.VsScheduler_OP
	default:
		return m.errf("lvs_sched: unknown scheduler %q", tokens[1])
	}
	return nil
}

func (m *parser) openReal(tokens []string) error {
	if len(tokens) < 4 || tokens[len(tokens)-1] != "{" {
		return m.errf("malformed real_server: expected 'real_server <ip> <port> {'")
	}

	ip, err := m.parseIP(tokens[1])
	if err != nil {
		return err
	}
	port, err := m.parsePort(tokens[2])
	if err != nil {
		return err
	}

	key := RealKey{IP: ip, Port: port}
	if _, dup := m.currVS.realsByKey[key]; dup {
		return m.errf("duplicate real_server %s:%d under virtual_server %s:%d",
			tokens[1], port, m.currVS.AddrText, m.currVS.Key.Port)
	}

	m.currRS = &RealServer{
		Key:      key,
		AddrText: tokens[1],
		// keepalived defaults a missing "weight" directive to 1; the
		// repo corpora always include it but stay defensive.
		Weight: 1,
		Line:   m.lineNo,
	}
	return nil
}

func (m *parser) handleWeight(tokens []string) error {
	if len(tokens) != 2 {
		return m.errf("weight: expected 'weight <n>'")
	}
	w, err := strconv.ParseUint(tokens[1], 10, 32)
	if err != nil {
		return m.errf("weight: invalid value %q: %v", tokens[1], err)
	}
	m.currRS.Weight = uint32(w)
	return nil
}

func (m *parser) closeReal() error {
	m.currVS.realsByKey[m.currRS.Key] = struct{}{}
	m.currVS.Reals = append(m.currVS.Reals, m.currRS)
	m.currRS = nil
	return nil
}

func (m *parser) closeVS() error {
	vs := m.currVS
	if len(vs.Reals) == 0 {
		return fmt.Errorf("parse services corpus %q:%d: virtual_server %s:%d has zero real_servers",
			m.source, vs.Line, vs.AddrText, vs.Key.Port)
	}
	if existing, dup := m.corpus.vsByKey[vs.Key]; dup {
		return fmt.Errorf(
			"parse services corpus %q:%d: duplicate virtual_server %s:%d (already defined at %s:%d)",
			m.source,
			vs.Line,
			vs.AddrText,
			vs.Key.Port,
			existing.Source,
			existing.Line,
		)
	}
	m.corpus.vsByKey[vs.Key] = vs
	m.corpus.VSs = append(m.corpus.VSs, vs)
	m.currVS = nil
	return nil
}

func (m *parser) parseIP(token string) ([16]byte, error) {
	var out [16]byte
	ip := net.ParseIP(token)
	if ip == nil {
		return out, m.errf("invalid IP literal %q", token)
	}
	v16 := ip.To16()
	if v16 == nil {
		return out, m.errf("invalid IP literal %q", token)
	}
	copy(out[:], v16)
	return out, nil
}

func (m *parser) parsePort(token string) (uint16, error) {
	v, err := strconv.ParseUint(token, 10, 32)
	if err != nil {
		return 0, m.errf("invalid port %q: %v", token, err)
	}
	if v == 0 || v > 65535 {
		return 0, m.errf("port %d out of range (1..65535)", v)
	}
	return uint16(v), nil
}

func (m *parser) errf(format string, args ...any) error {
	return fmt.Errorf("parse services corpus %q:%d: "+format,
		append([]any{m.source, m.lineNo}, args...)...)
}

// tokenize splits a stripped line into whitespace-separated tokens while
// guaranteeing that '{' and '}' are always standalone tokens even when
// the source glues them to neighbouring text. This lets splitStatements
// treat braces as unambiguous statement boundaries.
func tokenize(line string) []string {
	if line == "" {
		return nil
	}
	var b strings.Builder
	b.Grow(len(line) + 8)
	for idx := 0; idx < len(line); idx++ {
		c := line[idx]
		if c == '{' || c == '}' {
			b.WriteByte(' ')
			b.WriteByte(c)
			b.WriteByte(' ')
			continue
		}
		b.WriteByte(c)
	}
	return strings.Fields(b.String())
}

// splitStatements groups tokens into one statement per directive. A '{'
// closes the preceding directive and is attached to it (so block
// openers like "real_server 10.0.0.2 80 {" stay one statement); a '}'
// always emits as its own single-token statement. This turns
// "real_server 10.0.0.2 80 { weight 1 }" into three statements:
// {real_server 10.0.0.2 80 {}, {weight 1}, {}}.
func splitStatements(tokens []string) [][]string {
	if len(tokens) == 0 {
		return nil
	}
	out := make([][]string, 0, 2)
	start := 0
	for idx, tok := range tokens {
		switch tok {
		case "{":
			out = append(out, tokens[start:idx+1])
			start = idx + 1
		case "}":
			if start < idx {
				out = append(out, tokens[start:idx])
			}
			out = append(out, tokens[idx:idx+1])
			start = idx + 1
		}
	}
	if start < len(tokens) {
		out = append(out, tokens[start:])
	}
	return out
}

// stripComment removes the first unquoted '#' and everything after it.
// Quoted strings are preserved so directives like
//
//	quorum_up "/etc/keepalived/quorum-handler2.sh up addr,80/TCP,b-100,1"
//
// — which embed '#'-like characters inside their argument — are tokenised
// correctly. We do not interpret backslash escapes since the corpora do
// not use them.
func stripComment(line string) string {
	inQuote := false
	for idx := 0; idx < len(line); idx++ {
		c := line[idx]
		switch c {
		case '"':
			inQuote = !inQuote
		case '#':
			if !inQuote {
				return line[:idx]
			}
		}
	}
	return line
}
