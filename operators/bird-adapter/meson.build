install_data(
    'etc/yanet/bird-adapter-default.yaml',
    install_dir: '/etc/yanet2',
)

bird_adapter_ld_flags = '-X github.com/yanet-platform/yanet2/operators/bird-adapter/internal/version.version=' + yanet_version

# Skip bird-adapter when fuzzing — it does not use CGO but still gets sanitizer
# flags that interfere with the fuzzing build.
if not get_option('fuzzing').enabled()
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
      env: yanet_go_env,
      build_by_default: true,
      build_always_stale: true,
      depends: [
          bird_adapter_protoc_gen,
          common_protoc_gen,
          filter_protoc_gen,
          route_mpls_protoc_gen,
          common_readiness_protoc_gen,
          route_operator_protoc_gen,
          route_operator_readiness_protoc_gen,
      ],
      install: true,
      install_dir: get_option('bindir'),
  )
endif
