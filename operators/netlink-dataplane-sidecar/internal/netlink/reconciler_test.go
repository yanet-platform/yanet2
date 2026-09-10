package netlink_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
	vnetlink "github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	netreconcile "github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netlink"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netplan"
)

// fakeBackend models persistent interface state and injectable kernel failures.
type fakeBackend struct {
	Links          map[string]vnetlink.Link
	Addresses      map[string][]vnetlink.Addr
	Settings       map[string]string
	Failures       map[string]error
	Operations     []string
	BeforeLookup   func(string)
	BeforeAddrList func(string)
	BeforeSysctl   func(string, string)
	NextIndex      int
}

// newFakeBackend creates an empty namespace with stateful IPv6 configuration.
func newFakeBackend() *fakeBackend {
	return &fakeBackend{
		Links: map[string]vnetlink.Link{}, Addresses: map[string][]vnetlink.Addr{},
		Settings: map[string]string{}, Failures: map[string]error{}, NextIndex: 100,
	}
}

func (m *fakeBackend) LinkList() ([]vnetlink.Link, error) {
	links := []vnetlink.Link{}
	for _, link := range m.Links {
		links = append(links, link)
	}
	sort.Slice(links, func(first, second int) bool {
		return links[first].Attrs().Name < links[second].Attrs().Name
	})
	return links, m.Failures["links"]
}

func (m *fakeBackend) LinkByName(name string) (vnetlink.Link, error) {
	if m.BeforeLookup != nil {
		m.BeforeLookup(name)
	}
	link, present := m.Links[name]
	if !present {
		return nil, fmt.Errorf("missing link %q", name)
	}
	return link, nil
}

func (m *fakeBackend) LinkAdd(link vnetlink.Link) error {
	name := link.Attrs().Name
	if err := m.Failures["create:"+name]; err != nil {
		return err
	}
	if m.Links[name] != nil {
		return errors.New("link already exists")
	}
	if vlan, ok := link.(*vnetlink.Vlan); ok {
		parent := m.linkByIndex(vlan.ParentIndex)
		if parent == nil {
			return errors.New("missing VLAN parent")
		}
		if vlan.MTU == 0 {
			vlan.MTU = parent.Attrs().MTU
		}
		if vlan.MTU > parent.Attrs().MTU {
			return errors.New("VLAN MTU exceeds parent")
		}
	}
	if link.Attrs().MTU == 0 {
		link.Attrs().MTU = 1500
	}
	link.Attrs().Index = m.NextIndex
	m.NextIndex++
	m.Links[name] = link
	m.Operations = append(m.Operations, "create:"+name)
	return nil
}

func (m *fakeBackend) LinkSetMTU(link vnetlink.Link, mtu int) error {
	if err := m.Failures["mtu:"+link.Attrs().Name]; err != nil {
		return err
	}
	if link.Type() == "vlan" {
		parent := m.linkByIndex(link.Attrs().ParentIndex)
		if parent == nil || mtu > parent.Attrs().MTU {
			return errors.New("VLAN MTU exceeds parent")
		}
	}
	for _, child := range m.Links {
		if child.Type() == "vlan" && child.Attrs().ParentIndex == link.Attrs().Index && child.Attrs().MTU > mtu {
			child.Attrs().MTU = mtu
		}
	}
	link.Attrs().MTU = mtu
	m.Operations = append(m.Operations, "mtu:"+link.Attrs().Name)
	return nil
}

func (m *fakeBackend) linkByIndex(index int) vnetlink.Link {
	for _, link := range m.Links {
		if link.Attrs().Index == index {
			return link
		}
	}
	return nil
}

func (m *fakeBackend) LinkSetUp(link vnetlink.Link) error {
	name := link.Attrs().Name
	if err := m.Failures["up:"+name]; err != nil {
		return err
	}
	if link.Attrs().ParentIndex != 0 {
		parent := m.linkByIndex(link.Attrs().ParentIndex)
		if parent == nil || parent.Attrs().Flags&net.FlagUp == 0 {
			return errors.New("parent is down")
		}
	}
	link.Attrs().Flags |= net.FlagUp
	if m.Settings[name+"/addr_gen_mode"] != "1" && link.Attrs().Flags&net.FlagLoopback == 0 {
		m.Addresses[name] = append(m.Addresses[name], mustAddr("fe80::abcd/64"))
	}
	m.Operations = append(m.Operations, "up:"+name)
	return nil
}

func (m *fakeBackend) AddrList(link vnetlink.Link, family int) ([]vnetlink.Addr, error) {
	name := link.Attrs().Name
	if m.BeforeAddrList != nil {
		m.BeforeAddrList(name)
	}
	addresses := []vnetlink.Addr{}
	for _, address := range m.Addresses[name] {
		if family == vnetlink.FAMILY_ALL || family == vnetlink.FAMILY_V6 && address.IP.To4() == nil {
			addresses = append(addresses, address)
		}
	}
	return addresses, m.Failures["addresses:"+name]
}

func (m *fakeBackend) AddrReplace(link vnetlink.Link, address *vnetlink.Addr) error {
	name := link.Attrs().Name
	if err := m.Failures["address:"+address.String()]; err != nil {
		return err
	}
	m.Addresses[name] = append(m.Addresses[name], *address)
	m.Operations = append(m.Operations, "address:"+name+":"+address.String())
	return nil
}

func (m *fakeBackend) AddrDel(link vnetlink.Link, address *vnetlink.Addr) error {
	name := link.Attrs().Name
	if err := m.Failures["delete-address:"+address.String()]; err != nil {
		return err
	}
	m.Addresses[name] = slices.DeleteFunc(m.Addresses[name], func(candidate vnetlink.Addr) bool {
		return candidate.String() == address.String()
	})
	m.Operations = append(m.Operations, "delete-address:"+name+":"+address.String())
	return nil
}

// Test_Reconciler_RejectsNonEthernetKNI verifies that TUN and veth objects cannot
// acquire managed addresses or IPv6 settings through a matching interface name.
func Test_Reconciler_RejectsNonEthernetKNI(t *testing.T) {
	for _, test := range []struct {
		name string
		link vnetlink.Link
	}{
		{name: "TUN interface", link: &vnetlink.Tuntap{LinkAttrs: baseLink("kni0", 1).LinkAttrs, Mode: vnetlink.TUNTAP_MODE_TUN}},
		{name: "unknown tuntap mode", link: &vnetlink.Tuntap{LinkAttrs: baseLink("kni0", 1).LinkAttrs}},
		{name: "veth interface", link: &vnetlink.Veth{LinkAttrs: baseLink("kni0", 1).LinkAttrs}},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := newFakeBackend()
			backend.Links["kni0"] = test.link
			err := netreconcile.NewReconciler(backend, backend).Apply(t.Context(), netplan.State{Links: []netplan.Link{{Name: "kni0"}}})
			require.Error(t, err)
			require.Empty(t, backend.Operations)
			require.Empty(t, backend.Settings)
		})
	}
}

// Test_Reconciler_PolicyBeforeIPv6Enable verifies that an already-up interface
// cannot generate link-local addresses or accept RAs under inherited settings.
func Test_Reconciler_PolicyBeforeIPv6Enable(t *testing.T) {
	backend := newFakeBackend()
	link := baseLink("kni0", 1)
	link.Flags = net.FlagUp
	backend.Links["kni0"] = link
	backend.Settings["kni0/disable_ipv6"] = "1"
	backend.Settings["kni0/addr_gen_mode"] = "0"
	backend.Settings["kni0/accept_ra"] = "1"
	checked := false
	backend.BeforeSysctl = func(name, setting string) {
		if setting == "disable_ipv6" {
			checked = true
			require.Equal(t, "1", backend.Settings[name+"/addr_gen_mode"])
			require.Equal(t, "0", backend.Settings[name+"/accept_ra"])
		}
	}
	acceptRA := false
	state := netplan.State{Links: []netplan.Link{{Name: "kni0", AcceptRA: &acceptRA}}}
	require.NoError(t, netreconcile.NewReconciler(backend, backend).Apply(t.Context(), state))
	require.True(t, checked)
}

// Test_Reconciler_RepairsFailedDAD verifies that a failed explicit global or
// link-local IPv6 address is replaced rather than mistaken for converged state.
func Test_Reconciler_RepairsFailedDAD(t *testing.T) {
	for _, prefix := range []string{"fe80::f1/64", "2001:db8::1/64"} {
		t.Run(prefix, func(t *testing.T) {
			backend := newFakeBackend()
			backend.Links["kni0"] = baseLink("kni0", 1)
			failed := mustAddr(prefix)
			failed.Flags = unix.IFA_F_DADFAILED
			backend.Addresses["kni0"] = []vnetlink.Addr{failed}
			state := netplan.State{Links: []netplan.Link{{Name: "kni0", Addresses: []netip.Prefix{netip.MustParsePrefix(prefix)}}}}
			require.NoError(t, netreconcile.NewReconciler(backend, backend).Apply(t.Context(), state))
			require.Equal(t, []vnetlink.Addr{mustAddr(prefix)}, backend.Addresses["kni0"])
			require.Less(t, slices.Index(backend.Operations, "delete-address:kni0:"+prefix), slices.Index(backend.Operations, "address:kni0:"+prefix))
		})
	}
}

// Test_Reconciler_FailedDADRepairError verifies that either repair failure
// remains a failed apply and cannot authorize unlisted-address cleanup.
func Test_Reconciler_FailedDADRepairError(t *testing.T) {
	for _, operation := range []string{"delete-address:fe80::f1/64", "address:fe80::f1/64"} {
		t.Run(operation, func(t *testing.T) {
			backend := newFakeBackend()
			backend.Links["kni0"] = baseLink("kni0", 1)
			failed := mustAddr("fe80::f1/64")
			failed.Flags = unix.IFA_F_DADFAILED
			unlisted := mustAddr("fe80::abcd/64")
			backend.Addresses["kni0"] = []vnetlink.Addr{failed, unlisted}
			injected := errors.New("repair failed")
			backend.Failures[operation] = injected
			state := netplan.State{Links: []netplan.Link{{Name: "kni0", Addresses: []netip.Prefix{netip.MustParsePrefix("fe80::f1/64")}}}}
			require.ErrorIs(t, netreconcile.NewReconciler(backend, backend).Apply(t.Context(), state), injected)
			require.Contains(t, backend.Addresses["kni0"], unlisted)
		})
	}
}

// Test_Reconciler_TentativeAddressBlocksConvergence verifies that pending DAD
// waits for another observation without repeatedly deleting the desired address.
func Test_Reconciler_TentativeAddressBlocksConvergence(t *testing.T) {
	backend := newFakeBackend()
	link := baseLink("kni0", 1)
	link.Flags = net.FlagUp
	backend.Links["kni0"] = link
	address := mustAddr("fe80::f1/64")
	address.Flags = unix.IFA_F_TENTATIVE
	backend.Addresses["kni0"] = []vnetlink.Addr{address}
	state := netplan.State{Links: []netplan.Link{{Name: "kni0", Addresses: []netip.Prefix{netip.MustParsePrefix("fe80::f1/64")}}}}
	require.ErrorContains(t, netreconcile.NewReconciler(backend, backend).Apply(t.Context(), state), "duplicate address detection")
	require.Empty(t, backend.Operations)
}

// Test_Reconciler_CancellationDuringLookup verifies that cancellation at the
// final identity lookup prevents the next otherwise necessary MTU mutation.
func Test_Reconciler_CancellationDuringLookup(t *testing.T) {
	backend := newFakeBackend()
	backend.Links["kni0"] = baseLink("kni0", 1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	backend.BeforeLookup = func(string) { cancel() }
	state := netplan.State{Links: []netplan.Link{{Name: "kni0", MTU: 9000}}}
	require.ErrorIs(t, netreconcile.NewReconciler(backend, backend).Apply(ctx, state), context.Canceled)
	require.Equal(t, 1500, backend.Links["kni0"].Attrs().MTU)
	require.Empty(t, backend.Operations)
}

func (m *fakeBackend) SetIPv6(ctx context.Context, name, setting, value string, validate func() error) error {
	if m.BeforeSysctl != nil {
		m.BeforeSysctl(name, setting)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validate(); err != nil {
		return err
	}
	if err := m.Failures["sysctl:"+name]; err != nil {
		return err
	}
	m.Settings[name+"/"+setting] = value
	return nil
}

// baseLink provides a KNI-like kernel Ethernet with a valid IPv6 MTU.
func baseLink(name string, index int) *vnetlink.Device {
	return &vnetlink.Device{LinkAttrs: vnetlink.LinkAttrs{
		Name: name, Index: index, MTU: 1500,
		HardwareAddr: net.HardwareAddr{2, 0, 0, 0, 0, byte(index)},
	}}
}

// mustAddr creates a valid address fixture, preserving host bits.
func mustAddr(value string) vnetlink.Addr {
	address, err := vnetlink.ParseAddr(value)
	if err != nil {
		panic(err)
	}
	return *address
}

var _ netreconcile.Backend = (*fakeBackend)(nil)
var _ netreconcile.Sysctl = (*fakeBackend)(nil)

// addressStrings normalizes kernel IP representations to address/prefix pairs.
func addressStrings(addresses []vnetlink.Addr) []string {
	values := make([]string, len(addresses))
	for idx, address := range addresses {
		values[idx] = address.String()
	}
	return values
}

// fixtureNamespace supplies kernel-created base links for the public fixture.
func fixtureNamespace(t *testing.T) (*fakeBackend, netplan.State) {
	t.Helper()
	state, err := netplan.ParseFile("../netplan/testdata/dataplane.yaml")
	require.NoError(t, err)
	backend := newFakeBackend()
	backend.Links["kni0"] = baseLink("kni0", 2)
	backend.Links["kni1"] = baseLink("kni1", 3)
	loopback := baseLink("lo", 1)
	loopback.Flags = net.FlagLoopback
	backend.Links["lo"] = loopback
	return backend, state
}

// Test_Reconciler_RestoresImmutableTopology verifies that the same snapshot
// restores links, addresses, MTU, administrative state and sysctls after drift.
func Test_Reconciler_RestoresImmutableTopology(t *testing.T) {
	for _, mutation := range []string{"idempotent", "missing VLAN", "missing dummy", "recreated KNI", "configuration drift"} {
		t.Run(mutation, func(t *testing.T) {
			backend, state := fixtureNamespace(t)
			state.Links = append(state.Links, netplan.Link{
				Name: "loop1", Kind: netplan.LinkKindDummy, MTU: 9000,
				Addresses: []netip.Prefix{netip.MustParsePrefix("198.51.100.1/32")},
			})
			reconciler := netreconcile.NewReconciler(backend, backend)
			require.NoError(t, reconciler.Apply(t.Context(), state))
			backend.Operations = nil
			switch mutation {
			case "missing VLAN":
				delete(backend.Links, "kni0.802")
				delete(backend.Addresses, "kni0.802")
			case "missing dummy":
				delete(backend.Links, "loop1")
				delete(backend.Addresses, "loop1")
			case "recreated KNI":
				for name, link := range backend.Links {
					if link.Attrs().ParentIndex == 2 {
						delete(backend.Links, name)
						delete(backend.Addresses, name)
					}
				}
				backend.Links["kni0"] = baseLink("kni0", 50)
			case "configuration drift":
				for _, link := range backend.Links {
					link.Attrs().MTU = 1500
					link.Attrs().Flags &^= net.FlagUp
				}
				backend.Addresses = map[string][]vnetlink.Addr{}
				backend.Settings = map[string]string{}
			}
			require.NoError(t, reconciler.Apply(t.Context(), state))
			if mutation == "idempotent" {
				require.Empty(t, backend.Operations)
			}
			require.Len(t, backend.Links, len(state.Links))
			for _, wanted := range state.Links {
				link := backend.Links[wanted.Name]
				require.Equal(t, wanted.MTU, link.Attrs().MTU)
				require.NotZero(t, link.Attrs().Flags&net.FlagUp)
				require.Equal(t, "0", backend.Settings[wanted.Name+"/disable_ipv6"])
				for _, prefix := range wanted.Addresses {
					require.Contains(t, addressStrings(backend.Addresses[wanted.Name]), prefix.String())
				}
			}
		})
	}
}

// Test_Reconciler_ParentOrdering verifies that parent activation and both MTU
// directions work when the child sorts before its parent by name.
func Test_Reconciler_ParentOrdering(t *testing.T) {
	for _, initialMTU := range []int{1500, 9200} {
		t.Run(fmt.Sprint(initialMTU), func(t *testing.T) {
			backend := newFakeBackend()
			parent := baseLink("kni0", 1)
			parent.MTU = initialMTU
			backend.Links["kni0"] = parent
			if initialMTU > 9000 {
				backend.Links["aaa"] = &vnetlink.Vlan{
					LinkAttrs: vnetlink.LinkAttrs{Name: "aaa", Index: 2, ParentIndex: 1, MTU: initialMTU},
					VlanId:    0, VlanProtocol: vnetlink.VLAN_PROTOCOL_8021Q,
				}
			}
			if initialMTU < 9000 {
				backend.Links["aaa"] = &vnetlink.Vlan{
					LinkAttrs: vnetlink.LinkAttrs{Name: "aaa", Index: 2, ParentIndex: 1, MTU: initialMTU},
					VlanId:    0, VlanProtocol: vnetlink.VLAN_PROTOCOL_8021Q,
				}
			}
			state := netplan.State{Links: []netplan.Link{
				{Name: "aaa", Kind: netplan.LinkKindVLAN, Parent: "kni0", MTU: 9000},
				{Name: "kni0", MTU: 9000},
			}}
			require.NoError(t, netreconcile.NewReconciler(backend, backend).Apply(t.Context(), state))
			require.Equal(t, 9000, parent.MTU)
			require.Equal(t, 9000, backend.Links["aaa"].Attrs().MTU)
			parentChange := slices.Index(backend.Operations, "mtu:kni0")
			childChange := slices.Index(backend.Operations, "mtu:aaa")
			require.NotEqual(t, -1, parentChange)
			require.NotEqual(t, -1, childChange)
			if initialMTU < 9000 {
				require.Less(t, parentChange, childChange)
			} else {
				require.Less(t, childChange, parentChange)
			}
		})
	}
}

// Test_Reconciler_PreservesUnspecifiedState verifies that only unlisted IPv6LL
// is removed, after explicit addresses exist, regardless of permanent flags.
func Test_Reconciler_PreservesUnspecifiedState(t *testing.T) {
	backend := newFakeBackend()
	backend.Links["kni0"] = baseLink("kni0", 1)
	backend.Links["eth0"] = baseLink("eth0", 2)
	automatic := mustAddr("fe80::abcd/64")
	automatic.Flags = 128
	backend.Addresses["kni0"] = []vnetlink.Addr{mustAddr("192.0.2.1/24"), mustAddr("2001:db8::1/64"), automatic}
	backend.Addresses["eth0"] = []vnetlink.Addr{automatic}
	state := netplan.State{Links: []netplan.Link{{Name: "kni0", Addresses: []netip.Prefix{netip.MustParsePrefix("fe80::f1/64")}}}}
	require.NoError(t, netreconcile.NewReconciler(backend, backend).Apply(t.Context(), state))
	require.ElementsMatch(t, []vnetlink.Addr{mustAddr("192.0.2.1/24"), mustAddr("2001:db8::1/64"), mustAddr("fe80::f1/64")}, backend.Addresses["kni0"])
	require.Equal(t, []vnetlink.Addr{automatic}, backend.Addresses["eth0"])
	require.Equal(t, 1500, backend.Links["kni0"].Attrs().MTU)
	require.Equal(t, []string{"up:kni0", "address:kni0:fe80::f1/64", "delete-address:kni0:fe80::abcd/64"}, backend.Operations)
}

// Test_Reconciler_NewVLANUsesDesiredParentMTU verifies that initial inheritance
// cannot prevent an immutable parent's MTU decrease on every later retry.
func Test_Reconciler_NewVLANUsesDesiredParentMTU(t *testing.T) {
	backend := newFakeBackend()
	backend.Links["kni0"] = baseLink("kni0", 1)
	backend.Links["kni0"].Attrs().MTU = 9000
	state := netplan.State{Links: []netplan.Link{
		{Name: "kni0", MTU: 1500},
		{Name: "vlan0", Kind: netplan.LinkKindVLAN, Parent: "kni0", VLANID: 10},
	}}
	reconciler := netreconcile.NewReconciler(backend, backend)
	for range 2 {
		require.NoError(t, reconciler.Apply(t.Context(), state))
		require.Equal(t, 1500, backend.Links["kni0"].Attrs().MTU)
		require.Equal(t, 1500, backend.Links["vlan0"].Attrs().MTU)
	}
}

// Test_Reconciler_DummyIPv6LL verifies that disabling generation removes only
// unlisted link-local addresses on managed dummy interfaces.
func Test_Reconciler_DummyIPv6LL(t *testing.T) {
	backend := newFakeBackend()
	backend.Links["dummy0"] = &vnetlink.Dummy{LinkAttrs: baseLink("dummy0", 1).LinkAttrs}
	backend.Addresses["dummy0"] = []vnetlink.Addr{mustAddr("fe80::abcd/64"), mustAddr("2001:db8::1/128")}
	state := netplan.State{Links: []netplan.Link{{Name: "dummy0", Kind: netplan.LinkKindDummy, Addresses: []netip.Prefix{netip.MustParsePrefix("fe80::f1/64")}}}}
	require.NoError(t, netreconcile.NewReconciler(backend, backend).Apply(t.Context(), state))
	require.ElementsMatch(t, []string{"2001:db8::1/128", "fe80::f1/64"}, addressStrings(backend.Addresses["dummy0"]))
}

// Test_Reconciler_PartialFailureRetry verifies that a failed address does not
// roll back successful setup or permit IPv6LL cleanup before a complete retry.
func Test_Reconciler_PartialFailureRetry(t *testing.T) {
	backend := newFakeBackend()
	backend.Links["kni0"] = baseLink("kni0", 1)
	backend.Addresses["kni0"] = []vnetlink.Addr{mustAddr("fe80::abcd/64")}
	state := netplan.State{Links: []netplan.Link{{Name: "kni0", MTU: 9000, Addresses: []netip.Prefix{
		netip.MustParsePrefix("192.0.2.1/24"), netip.MustParsePrefix("fe80::f1/64"),
	}}}}
	injected := errors.New("address rejected")
	backend.Failures["address:fe80::f1/64"] = injected
	reconciler := netreconcile.NewReconciler(backend, backend)
	require.ErrorIs(t, reconciler.Apply(t.Context(), state), injected)
	require.Contains(t, addressStrings(backend.Addresses["kni0"]), "192.0.2.1/24")
	require.Contains(t, backend.Addresses["kni0"], mustAddr("fe80::abcd/64"))
	delete(backend.Failures, "address:fe80::f1/64")
	require.NoError(t, reconciler.Apply(t.Context(), state))
	require.Len(t, backend.Addresses["kni0"], 2)
}

// Test_Reconciler_RejectsIncompleteOrReplacedState verifies that invalid dumps,
// identity changes and cancellation never authorize address cleanup.
func Test_Reconciler_RejectsIncompleteOrReplacedState(t *testing.T) {
	for _, failure := range []string{"late KNI", "wrong KNI type", "IPv6 prefix conflict", "link dump", "address dump", "replacement", "cancelled"} {
		t.Run(failure, func(t *testing.T) {
			backend := newFakeBackend()
			backend.Links["kni0"] = baseLink("kni0", 1)
			backend.Addresses["kni0"] = []vnetlink.Addr{mustAddr("fe80::f1/128"), mustAddr("fe80::abcd/64")}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			state := netplan.State{Links: []netplan.Link{{Name: "kni0"}}}
			switch failure {
			case "late KNI":
				delete(backend.Links, "kni0")
			case "wrong KNI type":
				backend.Links["kni0"] = &vnetlink.Dummy{LinkAttrs: *baseLink("kni0", 1).Attrs()}
			case "IPv6 prefix conflict":
				state.Links[0].Addresses = []netip.Prefix{netip.MustParsePrefix("fe80::f1/64")}
			case "link dump":
				backend.Failures["links"] = vnetlink.ErrDumpInterrupted
			case "address dump":
				backend.Failures["addresses:kni0"] = vnetlink.ErrDumpInterrupted
			case "replacement":
				backend.BeforeSysctl = func(string, string) { backend.Links["kni0"] = baseLink("kni0", 2) }
			case "cancelled":
				cancel()
			}
			require.Error(t, netreconcile.NewReconciler(backend, backend).Apply(ctx, state))
			require.Len(t, backend.Addresses["kni0"], 2)
		})
	}
}
