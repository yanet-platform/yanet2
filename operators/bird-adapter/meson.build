install_data(
    'etc/yanet/bird-adapter-default.yaml',
    install_dir: '/etc/yanet2',
)

bird_adapter_ld_flags = '-X github.com/yanet-platform/yanet2/operators/bird-adapter/internal/version.version=' + yanet_version

if yanet_go_binaries
  custom_target(
      'yanet-bird-adapter',
      output: 'yanet-bird-adapter',
      command: [
          go,
          'build',
          '-ldflags=' + bird_adapter_ld_flags,
          '-buildvcs=false',
          '-o', '@OUTPUT@',
          join_paths(meson.current_source_dir(), 'cmd', 'yanet-bird-adapter'),
      ],
      env: yanet_go_nocgo_env,
      build_by_default: true,
      build_always_stale: true,
      depends: go_proto_targets,
      install: true,
      install_dir: get_option('bindir'),
  )
endif
