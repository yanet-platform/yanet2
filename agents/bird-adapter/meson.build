subdir('adapterpb')

# Skip bird-adapter when fuzzing — it does not use CGO but still gets sanitizer
# flags that interfere with the fuzzing build.
if not get_option('fuzzing').enabled()
  custom_target(
      'yanet-bird-adapter',
      output: 'yanet-bird-adapter',
      command: [
          go,
          'build',
          '-ldflags=' + ld_flags,
          '-buildvcs=false',
          '-o', '@OUTPUT@',
          join_paths(meson.project_source_root(), 'controlplane', 'cmd', 'bird-adapter'),
      ],
      env: yanet_go_env,
      build_by_default: true,
      build_always_stale: true,
      depends: [
          ynpb_gen,
          bird_adapter_protoc_gen,
          route_protoc_gen,
          common_protoc_gen,
      ],
      install: true,
      install_dir: get_option('bindir'),
  )
endif
