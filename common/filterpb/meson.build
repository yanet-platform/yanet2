filter_proto_dir = meson.current_source_dir()
root_dir = meson.project_source_root()

filter_proto_files = [
    join_paths(filter_proto_dir, 'filter.proto'),
]

# Generate protobuf files
filter_protoc_gen = custom_target(
    'filter-protoc',
    output: [
        'filter.pb.go',
        'filter_grpc.pb.go',
    ],
    input: filter_proto_files,
    command: [
        protoc,
        '-I', filter_proto_dir,
        '-I', root_dir,
        '--go_out=paths=source_relative:' + filter_proto_dir,
        '--go-grpc_out=paths=source_relative:' + filter_proto_dir,
        '@INPUT@',
    ],
    build_by_default: true,
)
