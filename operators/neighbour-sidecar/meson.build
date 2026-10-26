if yanet_go_binaries
  # Built only to keep the sidecar compiling with the tree: its container image
  # builds its own copy, and no package installs it.
  custom_target(
      'yanet-neighbour-sidecar',
      output: 'yanet-neighbour-sidecar',
      command: [
          go,
          'build',
          '-buildvcs=false',
          '-o', '@OUTPUT@',
          join_paths(meson.current_source_dir(), 'cmd', 'yanet-neighbour-sidecar'),
      ],
      env: yanet_go_nocgo_env,
      build_by_default: true,
      build_always_stale: true,
      depends: go_proto_targets,
  )
endif
