filter_proto_dir = meson.current_source_dir()
root_dir = meson.project_source_root()

filter_proto_files = [
    join_paths(filter_proto_dir, 'v1', 'filter.proto'),
]

filter_protoc_gen = custom_target(
    'filter-protoc',
    output: [
        'filter.pb.go',
        'filter_grpc.pb.go',
    ],
    input: filter_proto_files,
    command: [
        protoc,
        '-I', root_dir,
        '--go_out=paths=source_relative:' + root_dir,
        '--go-grpc_out=paths=source_relative:' + root_dir,
        '@INPUT@',
    ],
    build_by_default: true,
)
