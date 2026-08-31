install_data(
    'etc/yanet/generic-operator.d/forward.yaml',
    'etc/yanet/generic-operator.d/decap.yaml',
    install_dir: '/etc/yanet2/generic-operator.d',
)

install_data(
    'etc/yanet/forward.d/vlan-phy.yaml',
    'etc/yanet/forward.d/phy-vlan.yaml',
    install_dir: '/etc/yanet2/forward.d',
)

install_data(
    'etc/yanet/decap.d/default.yaml',
    install_dir: '/etc/yanet2/decap.d',
)

# Not a fuzz target, and nothing in the fuzz suite runs it, so the
# fuzzing configuration skips it to save build time.
if not get_option('fuzzing').enabled()
  # The binary links every gateway-facing proto package, so it depends on
  # every protoc generator.
  custom_target(
      'yanet-generic-operator',
      output: 'yanet-generic-operator',
      command: [
          go,
          'build',
          '-buildvcs=false',
          '-o', '@OUTPUT@',
          join_paths(meson.current_source_dir(), 'cmd', 'yanet-generic-operator'),
      ],
      env: yanet_go_nocgo_env,
      build_by_default: true,
      build_always_stale: true,
      depends: [
          ynpb_gen,
          common_protoc_gen,
          common_readiness_protoc_gen,
          filter_protoc_gen,
          balancer_protoc_gen,
          blackhole_protoc_gen,
          decap_protoc_gen,
          dscp_protoc_gen,
          forward_protoc_gen,
          mirror_protoc_gen,
          nat64_protoc_gen,
          pdump_protoc_gen,
          plain_protoc_gen,
          route_protoc_gen,
          route_mpls_protoc_gen,
          trafgen_protoc_gen,
          unrdup_protoc_gen,
          vlan_protoc_gen,
      ],
      install: true,
      install_dir: get_option('bindir'),
  )
endif
