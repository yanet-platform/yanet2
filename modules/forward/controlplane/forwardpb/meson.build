proto_dir = meson.current_source_dir()
proto_files = [join_paths(proto_dir, 'forward.proto')]

protoc_gen = custom_target(
    'forward-protoc',
    output: ['forward.pb.go', 'forward_grpc.pb.go'],
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
forward_protoc_gen = protoc_gen
