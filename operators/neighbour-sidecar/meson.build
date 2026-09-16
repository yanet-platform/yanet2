if not get_option('fuzzing').enabled()
  custom_target(
      'yanet-neighbour-sidecar',
      output: 'yanet-neighbour-sidecar',
      command: [
          go,
          'build',
          '-ldflags=' + ld_flags,
          '-buildvcs=false',
          '-o', '@OUTPUT@',
          join_paths(meson.current_source_dir(), 'cmd', 'yanet-neighbour-sidecar'),
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
  )
endif
