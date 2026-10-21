lib_rte_bpf_candidates = []
foreach library : libdpdk.get_variable('dpdk_static_libraries')
  if library.name() == 'rte_bpf'
    lib_rte_bpf_candidates += library
  endif
endforeach
assert(lib_rte_bpf_candidates.length() == 1, 'Expected one DPDK BPF static library')
lib_rte_bpf = lib_rte_bpf_candidates[0]

cgo_packages += {
  'bindings/go/cerrors': {
    'libraries': [lib_errors],
  },
  'bindings/go/filter': {},
  'common/go/logging/clog': {
    'libraries': [lib_logging],
  },
  'controlplane/ffi': {
    'libraries': [
      lib_agent_cp,
      lib_config_cp,
      lib_counters,
      lib_config_dp,
      lib_pipeline_dp,
      lib_dev_plain_api_static,
      lib_dev_vlan_api_static,
      lib_logging,
      lib_errors,
      lib_agent_counters,
      lib_counter_pattern,
    ],
    'inputs': [librure],
  },
  'devices/plain/controlplane': {
    'libraries': [lib_dev_plain_api_static, lib_logging],
  },
  'devices/trafgen/bindings/go/ctrafgen': {
    'libraries': [lib_dev_trafgen_api],
  },
  'devices/vlan/controlplane': {
    'libraries': [lib_dev_vlan_api_static, lib_logging],
  },
  'devices/vxlan/controlplane': {
    'libraries': [lib_dev_vxlan_api, lib_logging],
  },
  'modules/acl/bindings/go/cacl': {
    'libraries': [lib_acl_cp, lib_classify_compiler, lib_logging, lib_fwstate_objects, lib_statemap],
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
    'libraries': [lib_forward_cp, lib_classify_compiler],
  },
  'modules/fwstate/bindings/go/cfwstate': {
    'libraries': [
      lib_fwstate_cp,
      lib_fwstate_objects,
      lib_statemap,
      lib_config_cp,
      lib_config_dp,
      lib_pipeline_dp,
      lib_counters,
    ],
  },
  'modules/l3b/bindings/go/cl3b': {
    'libraries': [lib_l3b_cp, lib_classify_compiler, lib_statemap],
  },
  'modules/mirror/bindings/go/cmirror': {
    'libraries': [lib_mirror_cp, lib_classify_compiler],
  },
  'modules/nat64/bindings/go/cnat64': {
    'libraries': [lib_nat64_cp, lib_logging],
  },
  'modules/pdump/controlplane': {
    'libraries': [lib_pdump_cp, lib_pcap_static],
  },
  'modules/route/bindings/go/croute': {
    'libraries': [lib_route_cp],
  },
  'modules/route-mpls/bindings/go/croutempls': {
    'libraries': [lib_route_mpls_cp, lib_filter_compiler],
  },
  'modules/unrdup/bindings/go/cunrdup': {
    'libraries': [lib_unrdup_cp, lib_filter_compiler],
  },
  'objects/fwstate/bindings/go/cfwstate': {
    'libraries': [lib_fwstate_objects, lib_statemap, lib_counters],
  },
  'objects/l3b/bindings/go/cl3bobject': {
    'libraries': [lib_l3b_objects, lib_classify_compiler, lib_statemap, lib_l3state, lib_counters],
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
      lib_fwstate_objects,
      lib_l3b_objects,
      lib_ring_objects,
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
      lib_pipeline_dp,
      lib_module_dp,
      lib_worker_dp,
      lib_config_dp,
      lib_packet_dp,
      lib_logging,
      lib_agent_cp,
      lib_config_cp,
      lib_counters,
      lib_errors,
      lib_classify_compiler,
      lib_filter_compiler,
      lib_fwstate,
      lib_statemap,
      lib_l3state,
      lib_lib_utils,
      lib_agent_counters,
      lib_counter_pattern,
    ],
    'inputs': [librure],
  },
  'common/go/dataplane': {
    'libraries': [lib_lib_utils, lib_packet_dp, lib_logging],
  },
  'common/go/testutils': {},
  'common/go/testutils/shm': {
    'libraries': [
      lib_config_dp,
      lib_module_dp,
      lib_packet_dp,
      lib_agent_cp,
      lib_config_cp,
      lib_filter_compiler,
      lib_statemap,
      lib_l3state,
      lib_counters,
      lib_errors,
      lib_logging,
    ],
  },
  'lib/utils/go': {
    'libraries': [lib_lib_utils, lib_packet_dp, lib_logging],
  },
  'modules/acl/tests/functional/fwtable': {
    'libraries': [lib_fwstate_objects, lib_statemap, lib_counters],
  },
  'modules/decap/tests/functional': {
    'libraries': [lib_config_cp],
  },
  'modules/dscp/tests/dataplane': {
    'libraries': [lib_dscp_dp, lib_packet_dp, lib_logging],
  },
  'modules/fwstate/tests/dataplane': {
    'libraries': [
      lib_dataplane_ut,
      lib_fwstate_dp,
      lib_fwstate_cp,
      lib_statemap,
      lib_fwstate_objects,
      lib_config_cp,
      lib_config_dp,
      lib_pipeline_dp,
      lib_counters,
      lib_worker_dp,
      lib_packet_dp,
      lib_fwstate,
      lib_logging,
      lib_errors,
    ],
  },
  'modules/fwstate/tests/functional': {
    'libraries': [lib_counters],
  },
  'modules/l3b/internal/testutils': {
    'libraries': [
      lib_l3b_dp,
      lib_l3b_objects,
      lib_classify_compiler,
      lib_config_dp,
      lib_module_dp,
      lib_packet_dp,
      lib_agent_cp,
      lib_config_cp,
      lib_filter_compiler,
      lib_statemap,
      lib_l3state,
      lib_counters,
      lib_errors,
      lib_logging,
    ],
  },
  'modules/l3b/tests/internal/probe': {
    'libraries': [lib_l3b_objects, lib_classify_compiler, lib_statemap, lib_l3state, lib_counters],
  },
  'modules/nat64/tests/dataplane': {
    'libraries': [lib_nat64_dp, lib_nat64_cp, lib_packet_dp, lib_logging, lib_errors],
  },
  'modules/pdump/internal/testutils': {
    'libraries': [lib_pdump_dp, lib_ring_objects, lib_rte_bpf],
  },
  'objects/ring/tests/ringtest': {
    'libraries': [lib_config_cp, lib_ringtest_writer, lib_ring_objects],
  },
  'tests/counters': {},
}
