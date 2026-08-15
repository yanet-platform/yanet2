install_data(
    'etc/yanet/yanet-route-operator-default.yaml',
    install_dir: '/etc/yanet2',
)

# Skip route-operator when fuzzing — no CGO but still gets sanitizer flags
# that interfere with the fuzzing build.
if not get_option('fuzzing').enabled()
  custom_target(
      'yanet-route-operator',
      output: 'yanet-route-operator',
      command: [
          go,
          'build',
          '-ldflags=' + ld_flags,
          '-buildvcs=false',
          '-o', '@OUTPUT@',
          join_paths(meson.current_source_dir(), 'cmd', 'yanet-route-operator'),
      ],
      env: yanet_go_env,
      build_by_default: true,
      build_always_stale: true,
      depends: [
          route_operator_protoc_gen,
          ynpb_gen,
          common_protoc_gen,
          route_protoc_gen,
          common_readiness_protoc_gen,
          route_operator_readiness_protoc_gen,

          # cgo linker deps, which meson cannot see: a cgo link line is a
          # plain string to it. The operator reaches controlplane/ffi for
          # one type in modules/route/controlplane and nothing else, so
          # moving that type to a leaf package leaves it pure Go and
          # retires this entry.
          lib_agent_counters,
          lib_counter_pattern,
      ] + librure_targets,
      install: true,
      install_dir: get_option('bindir'),
  )
endif
