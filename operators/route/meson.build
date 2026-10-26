install_data(
    'etc/yanet/yanet-route-operator-default.yaml',
    install_dir: '/etc/yanet2',
)

if yanet_go_binaries
  custom_target(
      'yanet-route-operator',
      output: 'yanet-route-operator',
      command: [
          go,
          'build',
          '-buildvcs=false',
          '-o', '@OUTPUT@',
          join_paths(meson.current_source_dir(), 'cmd', 'yanet-route-operator'),
      ],
      env: yanet_go_nocgo_env,
      build_by_default: true,
      build_always_stale: true,
      depends: go_proto_targets,
      install: true,
      install_dir: get_option('bindir'),
  )
endif
