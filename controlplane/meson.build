subdir('ynpb')

ld_flags = '-X github.com/yanet-platform/yanet2/controlplane/internal/version.version=' + meson.project_version()

custom_target(
    'yanet-controlplane',
    output: 'yanet-controlplane',
    command: [
        go,
        'build',
        '-ldflags=' + ld_flags,
        '-o', '@OUTPUT@',
        join_paths(meson.current_source_dir(), 'cmd', 'yncp-director', 'main.go'),
    ],
    env: yanet_go_env,
    build_by_default: true,
    build_always_stale: true,
    depends: [
        # go proto gen deps
        ynpb_gen,
        decap_protoc_gen,
        dscp_protoc_gen,
        forward_protoc_gen,
        nat64_protoc_gen,
        route_protoc_gen,
        pdump_protoc_gen,
        balancer_protoc_gen,
        acl_protoc_gen,
        plain_protoc_gen,
        vlan_protoc_gen,
        proxy_protoc_gen,

        # cgo linker deps
        lib_acl_cp,
        lib_acl_dp,
        lib_balancer_cp,
        lib_balancer_dp,
        lib_decap_cp,
        lib_decap_dp,
        lib_dscp_cp,
        lib_dscp_dp,
        lib_forward_cp,
        lib_forward_dp,
        lib_fwstate_cp,
        lib_fwstate_dp,
        lib_nat64_cp,
        lib_nat64_dp,
        lib_pdump_cp,
        lib_pdump_dp,
        lib_route_cp,
        lib_route_dp,
        lib_filter_compiler,
    ],
    install: true,
    install_dir: get_option('bindir'),
)

# Skip bird-adapter when fuzzing - it doesn't use CGO but still gets sanitizer flags
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
          join_paths(meson.current_source_dir(), 'cmd', 'bird-adapter'),
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
