proto_dir = meson.current_source_dir()
root_dir = meson.project_source_root()
proto_files = [join_paths(proto_dir, 'plain.proto')]

protoc_gen = custom_target(
    'plain-protoc',
    output: ['plain.pb.go', 'plain_grpc.pb.go'],
    input: proto_files,
    command: [
        protoc,
        '-I', proto_dir,
        '-I', root_dir,
        '--go_out=paths=source_relative:' + proto_dir,
        '--go-grpc_out=paths=source_relative:' + proto_dir,
        '@INPUT@',
    ],
    build_by_default: true,
)
plain_protoc_gen = protoc_gen
