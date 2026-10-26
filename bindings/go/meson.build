dpdk_static_libraries = {}
foreach library : libdpdk.get_variable('dpdk_static_libraries')
  dpdk_static_libraries += {library.name(): library}
endforeach
lib_rte_bpf = dpdk_static_libraries['rte_bpf']
# The system libraries DPDK archives call into.
dpdk_extra_ldflags = libdpdk.get_variable('dpdk_extra_ldflags')

# The DPDK code that cgo test packages linking dataplane libraries call into.
#
# Mbuf, mempool and the EAL parts they reference suffice: the harnesses never
# initialize EAL (no rte_eal_init, no hugepages), and the dataplane_ut mempool
# keeps objects on the heap behind its own ops (lib/dataplane_ut/mempool.h).
dpdk_runtime_libraries = []
foreach name : ['mbuf', 'mempool', 'ring', 'eal', 'kvargs', 'telemetry', 'log']
  dpdk_runtime_libraries += dpdk_static_libraries['rte_' + name]
endforeach

cgo_packages += {
  'bindings/go/cerrors': {
    'libraries': [lib_errors],
  },
  'bindings/go/filter': {},
  'common/go/logging/clog': {
    'libraries': [lib_logging],
  },
  'controlplane/ffi': {
    'libraries': [lib_dev_plain_api_static, lib_dev_vlan_api_static, lib_agent_counters],
  },
  'devices/plain/controlplane': {
    'libraries': [lib_dev_plain_api_static],
  },
  'devices/trafgen/bindings/go/ctrafgen': {
    'libraries': [lib_dev_trafgen_api],
  },
  'devices/vlan/controlplane': {
    'libraries': [lib_dev_vlan_api_static],
  },
  'devices/vxlan/controlplane': {
    'libraries': [lib_dev_vxlan_api],
  },
  'modules/acl/bindings/go/cacl': {
    'libraries': [lib_acl_cp, lib_fwstate_objects],
  },
  'modules/blackhole/bindings/go/cblackhole': {
    'libraries': [lib_blackhole_cp],
  },
  'modules/decap/bindings/go/cdecap': {
    'libraries': [lib_decap_cp],
  },
  'modules/dscp/bindings/go/cdscp': {
    'libraries': [lib_dscp_cp],
  },
  'modules/forward/bindings/go/cforward': {
    'libraries': [lib_forward_cp],
  },
  'modules/fwstate/bindings/go/cfwstate': {
    'libraries': [lib_fwstate_cp, lib_fwstate_objects],
  },
  'modules/l3b/bindings/go/cl3b': {
    'libraries': [lib_l3b_cp, lib_statemap],
  },
  'modules/mirror/bindings/go/cmirror': {
    'libraries': [lib_mirror_cp],
  },
  'modules/nat64/bindings/go/cnat64': {
    'libraries': [lib_nat64_cp],
  },
  'modules/pdump/controlplane': {
    'libraries': [lib_pdump_cp],
    'archives': [lib_rte_bpf, lib_pcap_static],
  },
  'modules/route/bindings/go/croute': {
    'libraries': [lib_route_cp],
  },
  'modules/route-mpls/bindings/go/croutempls': {
    'libraries': [lib_route_mpls_cp],
  },
  'modules/unrdup/bindings/go/cunrdup': {
    'libraries': [lib_unrdup_cp],
  },
  'objects/fwstate/bindings/go/cfwstate': {
    'libraries': [lib_fwstate_objects],
  },
  'objects/l3b/bindings/go/cl3bobject': {
    'libraries': [lib_l3b_objects, lib_l3state],
  },
  'objects/ring/bindings/go/cring': {
    'libraries': [lib_ring_objects],
  },
}

# Director preparation covers only its production package closure.
cgo_controlplane_packages = cgo_packages.keys()

cgo_packages += {
  'bindings/go/dataplane_ut': {
    'libraries': [
      lib_blackhole_dp,
      lib_l3b_dp,
      lib_decap_dp,
      lib_dscp_dp,
      lib_acl_dp,
      lib_fwstate_dp,
      lib_l3b_objects,
      lib_forward_dp,
      lib_mirror_dp,
      lib_route_dp,
      lib_route_mpls_dp,
      lib_nat64_dp,
      lib_pdump_dp,
      lib_unrdup_dp,
      lib_dev_plain_dp,
      lib_dev_vlan_dp,
      lib_dev_vxlan_dp,
      lib_dataplane_ut,
      lib_filter_compiler,
      lib_agent_counters,
    ],
    'archives': [lib_rte_bpf] + dpdk_runtime_libraries,
  },
  'common/go/dataplane': {
    'libraries': [lib_lib_utils],
  },
  'common/go/testutils': {},
  'common/go/testutils/shm': {
    'libraries': [
      lib_config_dp,
      lib_packet_dp,
      lib_agent_cp,
      lib_filter_compiler,
      lib_statemap,
      lib_l3state,
    ],
  },
  'lib/utils/go': {
    'libraries': [lib_lib_utils],
  },
  'modules/acl/tests/functional/fwtable': {
    'libraries': [lib_fwstate_objects],
  },
  'modules/decap/tests/functional': {
    'libraries': [lib_config_cp],
  },
  'modules/dscp/tests/dataplane': {
    'libraries': [lib_dscp_dp],
  },
  'modules/fwstate/tests/dataplane': {
    'libraries': [lib_dataplane_ut, lib_fwstate_dp, lib_fwstate_cp],
    'archives': dpdk_runtime_libraries,
  },
  'modules/fwstate/tests/functional': {
    'libraries': [lib_counters],
  },
  'modules/l3b/internal/testutils': {
    'libraries': [lib_l3b_dp, lib_l3b_objects, lib_config_dp, lib_agent_cp, lib_filter_compiler],
  },
  'modules/l3b/tests/internal/probe': {
    'libraries': [lib_l3b_objects, lib_l3state],
  },
  'modules/nat64/tests/dataplane': {
    'libraries': [lib_nat64_dp, lib_nat64_cp, lib_packet_dp],
  },
  'modules/pdump/internal/testutils': {
    'libraries': [lib_pdump_dp],
    'archives': [lib_rte_bpf],
  },
  'objects/ring/tests/ringtest': {
    'libraries': [lib_ringtest_writer, lib_ring_objects],
  },
  'tests/counters': {},
}
