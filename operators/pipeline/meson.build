subdir('operatorpb')

# Skip pipeline-operator when fuzzing — no CGO but still gets sanitizer flags
# that interfere with the fuzzing build.
if not get_option('fuzzing').enabled()
  custom_target(
      'yanet-pipeline-operator',
      output: 'yanet-pipeline-operator',
      command: [
          go,
          'build',
          '-ldflags=' + ld_flags,
          '-buildvcs=false',
          '-o', '@OUTPUT@',
          join_paths(meson.current_source_dir(), 'cmd', 'yanet-pipeline-operator'),
      ],
      env: yanet_go_env,
      build_by_default: true,
      build_always_stale: true,
      depends: [
          pipeline_operator_protoc_gen,
          ynpb_gen,
          common_protoc_gen,
          plain_protoc_gen,
          vlan_protoc_gen,
      ],
      install: true,
      install_dir: get_option('bindir'),
  )
endif
