install_data(
    'etc/yanet/forward.d/vlan-phy-default.yaml',
    'etc/yanet/forward.d/phy-vlan-default.yaml',
    install_dir: '/etc/yanet2/forward.d',
)

if not get_option('fuzzing').enabled()
  custom_target(
      'yanet-forward-operator',
      output: 'yanet-forward-operator',
      command: [
          go,
          'build',
          '-buildvcs=false',
          '-o', '@OUTPUT@',
          join_paths(meson.current_source_dir(), 'cmd', 'yanet-forward-operator'),
      ],
      env: yanet_go_env,
      build_by_default: true,
      build_always_stale: true,
      depends: [
          ynpb_gen,
          common_protoc_gen,
          forward_protoc_gen,
      ],
      install: true,
      install_dir: get_option('bindir'),
  )
endif
