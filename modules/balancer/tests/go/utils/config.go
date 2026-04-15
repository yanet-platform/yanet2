package utils

import (
	"math/rand"
	"net/netip"

	"github.com/yanet-platform/yanet2/common/filterpb"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	"google.golang.org/protobuf/types/known/durationpb"
)

// ---------------------------------------------------------------------------
// Default values
// ---------------------------------------------------------------------------

var (
	DefaultSourceV4 = netip.MustParseAddr("100.0.0.1")
	DefaultSourceV6 = netip.MustParseAddr("2001:db8::1")

	DefaultTimeouts = &balancerpb.SessionsTimeouts{
		TcpSynAck: 25,
		TcpSyn:    20,
		TcpFin:    15,
		Tcp:       60,
		Udp:       30,
	}

	DefaultSessionCapacity  uint64  = 20_000
	DefaultMaxLoadFactor    float32 = 0.5
	DefaultRefreshPeriodSec         = 0
	DefaultWlcPower         uint64  = 0
	DefaultWlcMaxWeight     uint32  = 0
)

// ---------------------------------------------------------------------------
// ConfigBuilder
// ---------------------------------------------------------------------------

type ConfigBuilder struct {
	sourceV4         netip.Addr
	sourceV6         netip.Addr
	vs               []*balancerpb.VirtualService
	timeouts         *balancerpb.SessionsTimeouts
	sessionCapacity  uint64
	maxLoadFactor    float32
	refreshPeriodSec int
	wlcPower         uint64
	wlcMaxWeight     uint32
}

func NewConfigBuilder() *ConfigBuilder {
	return &ConfigBuilder{
		sourceV4:         DefaultSourceV4,
		sourceV6:         DefaultSourceV6,
		timeouts:         DefaultTimeouts,
		sessionCapacity:  DefaultSessionCapacity,
		maxLoadFactor:    DefaultMaxLoadFactor,
		refreshPeriodSec: DefaultRefreshPeriodSec,
		wlcPower:         DefaultWlcPower,
		wlcMaxWeight:     DefaultWlcMaxWeight,
	}
}

func (b *ConfigBuilder) WithSourceV4(ip string) *ConfigBuilder {
	b.sourceV4 = netip.MustParseAddr(ip)
	return b
}

func (b *ConfigBuilder) WithSourceV6(ip string) *ConfigBuilder {
	b.sourceV6 = netip.MustParseAddr(ip)
	return b
}

func (b *ConfigBuilder) AddVS(vs ...*balancerpb.VirtualService) *ConfigBuilder {
	b.vs = append(b.vs, vs...)
	return b
}

func (b *ConfigBuilder) WithTimeouts(t *balancerpb.SessionsTimeouts) *ConfigBuilder {
	b.timeouts = t
	return b
}

func (b *ConfigBuilder) WithSessionCapacity(cap uint64) *ConfigBuilder {
	b.sessionCapacity = cap
	return b
}

func (b *ConfigBuilder) WithMaxLoadFactor(f float32) *ConfigBuilder {
	b.maxLoadFactor = f
	return b
}

func (b *ConfigBuilder) WithRefreshPeriod(seconds int) *ConfigBuilder {
	b.refreshPeriodSec = seconds
	return b
}

func (b *ConfigBuilder) WithWLC(power uint64, maxWeight uint32) *ConfigBuilder {
	b.wlcPower = power
	b.wlcMaxWeight = maxWeight
	return b
}

func (b *ConfigBuilder) Build() *balancerpb.BalancerConfig {
	cap := b.sessionCapacity
	mlf := b.maxLoadFactor
	wlcPower := b.wlcPower
	wlcMaxWeight := b.wlcMaxWeight

	return &balancerpb.BalancerConfig{
		PacketHandler: &balancerpb.PacketHandlerConfig{
			Vs:               b.vs,
			SourceAddressV4:  b.sourceV4.AsSlice(),
			SourceAddressV6:  b.sourceV6.AsSlice(),
			DecapAddresses:   [][]byte{},
			SessionsTimeouts: b.timeouts,
		},
		State: &balancerpb.StateConfig{
			SessionTableCapacity:      &cap,
			SessionTableMaxLoadFactor: &mlf,
			Wlc: &balancerpb.WlcConfig{
				Power:     &wlcPower,
				MaxWeight: &wlcMaxWeight,
			},
			RefreshPeriod: durationpb.New(0),
		},
	}
}

// ---------------------------------------------------------------------------
// VSBuilder
// ---------------------------------------------------------------------------

type VSBuilder struct {
	addr      netip.Addr
	port      uint16
	proto     balancerpb.TransportProto
	scheduler balancerpb.VsScheduler
	flags     balancerpb.VsFlags
	reals     []*balancerpb.Real
	allowed   []*balancerpb.AllowedSources
	peers     [][]byte
}

func NewTCPVS(addr string, port uint16) *VSBuilder {
	return &VSBuilder{
		addr:  netip.MustParseAddr(addr),
		port:  port,
		proto: balancerpb.TransportProto_TCP,
	}
}

func NewUDPVS(addr string, port uint16) *VSBuilder {
	return &VSBuilder{
		addr:  netip.MustParseAddr(addr),
		port:  port,
		proto: balancerpb.TransportProto_UDP,
	}
}

func (b *VSBuilder) WithScheduler(s balancerpb.VsScheduler) *VSBuilder {
	b.scheduler = s
	return b
}

func (b *VSBuilder) OPS() *VSBuilder {
	b.flags.Ops = true
	return b
}

func (b *VSBuilder) GRE() *VSBuilder {
	b.flags.Gre = true
	return b
}

func (b *VSBuilder) FixMSS() *VSBuilder {
	b.flags.FixMss = true
	return b
}

func (b *VSBuilder) PureL3() *VSBuilder {
	b.flags.PureL3 = true
	b.port = 0
	return b
}

func (b *VSBuilder) WLC() *VSBuilder {
	b.scheduler = balancerpb.VsScheduler_WLC
	return b
}

func (b *VSBuilder) AddReal(r ...*balancerpb.Real) *VSBuilder {
	b.reals = append(b.reals, r...)
	return b
}

// AllowAll adds a permissive allowed source (0.0.0.0/0 or ::/0 based on VS IP version).
func (b *VSBuilder) AllowAll() *VSBuilder {
	if b.addr.Is4() {
		b.allowed = append(b.allowed, &balancerpb.AllowedSources{
			Nets: []*filterpb.IPNet{
				{Addr: make([]byte, 4), Mask: make([]byte, 4)},
			},
		})
	} else {
		b.allowed = append(b.allowed, &balancerpb.AllowedSources{
			Nets: []*filterpb.IPNet{
				{Addr: make([]byte, 16), Mask: make([]byte, 16)},
			},
		})
	}
	return b
}

func (b *VSBuilder) AddAllowedSrc(src *balancerpb.AllowedSources) *VSBuilder {
	b.allowed = append(b.allowed, src)
	return b
}

func (b *VSBuilder) AddPeers(peers ...string) *VSBuilder {
	for _, p := range peers {
		addr := netip.MustParseAddr(p)
		b.peers = append(b.peers, addr.AsSlice())
	}
	return b
}

func (b *VSBuilder) Build() *balancerpb.VirtualService {
	return &balancerpb.VirtualService{
		Id: &balancerpb.VsIdentifier{
			Addr:  b.addr.AsSlice(),
			Port:  uint32(b.port),
			Proto: b.proto,
		},
		Scheduler:   b.scheduler,
		Flags:       &b.flags,
		Reals:       b.reals,
		AllowedSrcs: b.allowed,
		Peers:       b.peers,
	}
}

// ---------------------------------------------------------------------------
// Real constructors
// ---------------------------------------------------------------------------

// R creates a real with weight 1 and a full mask (preserves entire src address).
func R(addr string) *balancerpb.Real {
	return RW(addr, 1)
}

// RW creates a real with the given weight and a full mask.
func RW(addr string, weight uint32) *balancerpb.Real {
	ip := netip.MustParseAddr(addr)
	var fullMask []byte
	if ip.Is4() {
		fullMask = []byte{255, 255, 255, 255}
	} else {
		fullMask = []byte{
			0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
			0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
		}
	}
	return &balancerpb.Real{
		Id:     &balancerpb.RelativeRealIdentifier{Ip: ip.AsSlice()},
		Weight: weight,
		Src: &filterpb.IPNet{
			Addr: ip.AsSlice(),
			Mask: fullMask,
		},
	}
}

// RealWithSrc creates a real with custom source address and mask.
func RealWithSrc(addr string, weight uint32, srcAddr, srcMask string) *balancerpb.Real {
	ip := netip.MustParseAddr(addr)
	src := netip.MustParseAddr(srcAddr)
	mask := netip.MustParseAddr(srcMask)
	return &balancerpb.Real{
		Id:     &balancerpb.RelativeRealIdentifier{Ip: ip.AsSlice()},
		Weight: weight,
		Src: &filterpb.IPNet{
			Addr: src.AsSlice(),
			Mask: mask.AsSlice(),
		},
	}
}

// ---------------------------------------------------------------------------
// Generators
// ---------------------------------------------------------------------------

// GenerateReals generates count random real servers with random weights.
func GenerateReals(count int, rng *rand.Rand) []*balancerpb.Real {
	reals := make([]*balancerpb.Real, count)
	for i := range count {
		ip := generateRealIP(i, rng.Intn(2) == 0)
		weight := uint32(rng.Intn(100)) + 1
		reals[i] = RW(ip.String(), weight)
	}
	return reals
}

// GenerateVSList generates count virtual services with realsPerVS reals each.
func GenerateVSList(count, realsPerVS int, rng *rand.Rand) []*balancerpb.VirtualService {
	vsList := make([]*balancerpb.VirtualService, count)
	for i := range count {
		isV6 := rng.Intn(2) == 0
		isTCP := rng.Intn(2) == 0
		port := uint16(1000 + i)

		var addr netip.Addr
		if isV6 {
			addr = generateV6Addr(rng)
		} else {
			addr = generateV4Addr(10, rng)
		}

		var b *VSBuilder
		if isTCP {
			b = NewTCPVS(addr.String(), port)
		} else {
			b = NewUDPVS(addr.String(), port)
		}

		// Random scheduler
		if rng.Intn(2) == 0 {
			b.WithScheduler(balancerpb.VsScheduler_WRR)
		}

		// Random flags
		if rng.Intn(4) == 0 {
			b.GRE()
		}
		if rng.Intn(4) == 0 {
			b.OPS()
		}

		b.AllowAll()

		reals := make([]*balancerpb.Real, realsPerVS)
		for j := range realsPerVS {
			realIsV6 := rng.Intn(2) == 0
			realIP := generateRealIP(i*realsPerVS+j, realIsV6)
			weight := uint32(rng.Intn(100)) + 1
			reals[j] = RW(realIP.String(), weight)
		}
		b.AddReal(reals...)

		vsList[i] = b.Build()
	}
	return vsList
}

func generateV4Addr(prefix byte, rng *rand.Rand) netip.Addr {
	return netip.AddrFrom4([4]byte{
		prefix,
		byte(rng.Intn(256)),
		byte(rng.Intn(256)),
		byte(rng.Intn(254)) + 1,
	})
}

func generateV6Addr(rng *rand.Rand) netip.Addr {
	var b [16]byte
	b[0] = 0x20
	b[1] = 0x01
	b[2] = 0x0d
	b[3] = 0xb8
	for i := 4; i < 16; i++ {
		b[i] = byte(rng.Intn(256))
	}
	if b[15] == 0 {
		b[15] = 1
	}
	return netip.AddrFrom16(b)
}

func generateRealIP(index int, isV6 bool) netip.Addr {
	if isV6 {
		var b [16]byte
		b[0] = 0xfd
		b[1] = 0x00
		b[14] = byte(index >> 8)
		b[15] = byte(index&0xff) + 1
		return netip.AddrFrom16(b)
	}
	return netip.AddrFrom4([4]byte{
		192,
		168,
		byte(index >> 8),
		byte(index&0xff) + 1,
	})
}

// ---------------------------------------------------------------------------
// Helpers for building RealUpdate
// ---------------------------------------------------------------------------

// EnableReal creates a RealUpdate that enables a real.
func EnableReal(vsID *balancerpb.VsIdentifier, realID *balancerpb.RelativeRealIdentifier) *balancerpb.RealUpdate {
	enable := true
	return &balancerpb.RealUpdate{
		RealId: &balancerpb.RealIdentifier{
			Vs:   vsID,
			Real: realID,
		},
		Enable: &enable,
	}
}

// DisableReal creates a RealUpdate that disables a real.
func DisableReal(vsID *balancerpb.VsIdentifier, realID *balancerpb.RelativeRealIdentifier) *balancerpb.RealUpdate {
	enable := false
	return &balancerpb.RealUpdate{
		RealId: &balancerpb.RealIdentifier{
			Vs:   vsID,
			Real: realID,
		},
		Enable: &enable,
	}
}

// SetWeight creates a RealUpdate that changes a real's weight.
func SetWeight(vsID *balancerpb.VsIdentifier, realID *balancerpb.RelativeRealIdentifier, weight uint32) *balancerpb.RealUpdate {
	return &balancerpb.RealUpdate{
		RealId: &balancerpb.RealIdentifier{
			Vs:   vsID,
			Real: realID,
		},
		Weight: &weight,
	}
}

// ---------------------------------------------------------------------------
// Convenience: QuickConfig
// ---------------------------------------------------------------------------

// QuickConfig creates a BalancerConfig from virtual services with all defaults.
func QuickConfig(vs ...*balancerpb.VirtualService) *balancerpb.BalancerConfig {
	return NewConfigBuilder().AddVS(vs...).Build()
}

// QuickTestSetup is a shorthand for creating a single-worker test setup with sensible memory defaults.
func QuickTestSetup(config *balancerpb.BalancerConfig) *TestConfig {
	return &TestConfig{
		Mock:     SingleWorkerMockConfig(64*MB, 4*MB),
		Balancer: config,
	}
}

// Memory size constants for convenience.
const (
	MB = 1 << 20
)

// ---------------------------------------------------------------------------
// Address helpers
// ---------------------------------------------------------------------------

// Addr parses an IP address string, panicking on failure.
func Addr(s string) netip.Addr {
	return netip.MustParseAddr(s)
}

// AddrBytes returns the raw bytes of a parsed IP address.
func AddrBytes(s string) []byte {
	return netip.MustParseAddr(s).AsSlice()
}

// IPNet creates a filterpb.IPNet from address and mask strings.
func IPNet(addr, mask string) *filterpb.IPNet {
	return &filterpb.IPNet{
		Addr: AddrBytes(addr),
		Mask: AddrBytes(mask),
	}
}

// AllowedNet creates an AllowedSources entry from a network prefix.
func AllowedNet(addr, mask string) *balancerpb.AllowedSources {
	return &balancerpb.AllowedSources{
		Nets: []*filterpb.IPNet{IPNet(addr, mask)},
	}
}
