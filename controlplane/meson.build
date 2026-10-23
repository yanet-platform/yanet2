subdir('ynpb')

install_data(
    'etc/yanet/controlplane.d/default.yaml',
    install_dir: '/etc/yanet2/controlplane.d',
)

ld_flags = '-X github.com/yanet-platform/yanet2/controlplane/internal/version.version=' + yanet_version

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
    # Every proto generator and every native library the director links must
    # be ready first; the cgo stamps take those libraries as inputs.
    depends: go_proto_targets + cgo_controlplane_deps + librure_targets,
    install: true,
    install_dir: get_option('bindir'),
)
