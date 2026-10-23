install_data(
    'etc/yanet/generic-operator.d/forward-default.yaml',
    'etc/yanet/generic-operator.d/decap-default.yaml',
    install_dir: '/etc/yanet2/generic-operator.d',
)

install_data(
    'etc/yanet/forward.d/vlan-phy-default.yaml',
    'etc/yanet/forward.d/phy-vlan-default.yaml',
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
      depends: go_proto_targets,
      install: true,
      install_dir: get_option('bindir'),
  )
endif
