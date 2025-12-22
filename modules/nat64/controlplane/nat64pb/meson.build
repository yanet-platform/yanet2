proto_dir = meson.current_source_dir()
proto_files = [join_paths(proto_dir, 'nat64.proto')]

protoc_gen = custom_target(
    'nat64-protoc',
    output: ['nat64.pb.go', 'nat64_grpc.pb.go'],
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
nat64_protoc_gen = protoc_gen
