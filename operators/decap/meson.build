subdir('operatorpb')

install_data(
    'etc/yanet/decap.d/default.yaml',
    install_dir: '/etc/yanet2/decap.d',
)

install_data(
    'etc/yanet/yanet-decap-operator-default.yaml',
    install_dir: '/etc/yanet2',
)

if not get_option('fuzzing').enabled()
  custom_target(
      'yanet-decap-operator',
      output: 'yanet-decap-operator',
      command: [
          go,
          'build',
          '-buildvcs=false',
          '-o', '@OUTPUT@',
          join_paths(meson.current_source_dir(), 'cmd', 'yanet-decap-operator'),
      ],
      env: yanet_go_env,
      build_by_default: true,
      build_always_stale: true,
      depends: [
          ynpb_gen,
          common_protoc_gen,
          decap_protoc_gen,
          decap_operator_protoc_gen,
          readiness_protoc_gen,
      ],
      install: true,
      install_dir: get_option('bindir'),
  )
endif
