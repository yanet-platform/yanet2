subdir('coordinatorpb')

custom_target(
    'yanet-coordinator',
    output: 'yanet-coordinator',
    command: [
        go,
        'build',
        '-o', '@OUTPUT@',
        join_paths(meson.current_source_dir(), 'cmd', 'coordinator', 'main.go'),
    ],
    build_by_default: true,
    build_always_stale: true,
    depends: [
        coordinator_protoc_gen,
        ynpb_gen, # Add dependency to ensure ynpb files are generated
    ],
    install: true,
    install_dir: get_option('bindir'),
)
