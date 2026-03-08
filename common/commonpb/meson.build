common_proto_dir = meson.current_source_dir()

common_proto_files = [
    join_paths(common_proto_dir, 'target.proto'),
]

# Generate protobuf files
common_protoc_gen = custom_target(
    'common-protoc',
    output: [
        'target.pb.go',
        'target_grpc.pb.go',
    ],
    input: common_proto_files,
    command: [
        protoc,
        '-I', common_proto_dir,
        '--go_out=paths=source_relative:' + common_proto_dir,
        '--go-grpc_out=paths=source_relative:' + common_proto_dir,
        '@INPUT@',
    ],
    build_by_default: true,
)
