install_data(
    'etc/yanet/yanet-netlink-dataplane-sidecar-default.yaml',
    install_dir: '/etc/yanet2',
)

# Not a fuzz target, and nothing in the fuzz suite runs it, so the
# fuzzing configuration skips it to save build time.
if not get_option('fuzzing').enabled()
  custom_target(
      'yanet-netlink-dataplane-sidecar',
      output: 'yanet-netlink-dataplane-sidecar',
      command: [
          go,
          'build',
          '-ldflags=' + ld_flags,
          '-buildvcs=false',
          '-o', '@OUTPUT@',
          join_paths(
              meson.current_source_dir(),
              'cmd',
              'yanet-netlink-dataplane-sidecar',
          ),
      ],
      env: yanet_go_nocgo_env,
      build_by_default: true,
      build_always_stale: true,
      depends: [
          route_operator_protoc_gen,
          common_protoc_gen,
          ynpb_gen,
          common_readiness_protoc_gen,
          route_operator_readiness_protoc_gen,
      ],
      install: true,
      install_dir: get_option('bindir'),
  )
endif
