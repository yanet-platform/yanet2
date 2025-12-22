proto_dir = meson.current_source_dir()
proto_files = [
    join_paths(proto_dir, 'acl.proto'),
]

protoc_gen = custom_target(
    'acl-protoc',
    output: ['acl.pb.go', 'acl_grpc.pb.go'],
    input: proto_files,
    command: [
        protoc,
        '-I', proto_dir,
        '--go_out=paths=source_relative:' + proto_dir,
        '--go-grpc_out=paths=source_relative:' + proto_dir,
        '@INPUT@',
    ],
    build_by_default: true,
)
acl_protoc_gen = protoc_gen
