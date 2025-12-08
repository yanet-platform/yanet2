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
        ynpb_gen,
        decap_protoc_gen,
        dscp_protoc_gen,
        forward_protoc_gen,
        nat64_protoc_gen,
        route_protoc_gen,
        pdump_protoc_gen,
        balancer_protoc_gen,
        plain_protoc_gen,
        vlan_protoc_gen,
    ],
    install: true,
    install_dir: get_option('bindir'),
)

custom_target(
    'yanet-bird-adapter',
    output: 'yanet-bird-adapter',
    command: [
        go,
        'build',
        '-ldflags=' + ld_flags,
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
