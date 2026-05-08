proto_dir = meson.current_source_dir()
proto_files = [join_paths(proto_dir, 'bird_import.proto')]

protoc_gen = custom_target(
    'bird-adapter-protoc',
    output: ['bird_import.pb.go', 'bird_import_grpc.pb.go'],
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
bird_adapter_protoc_gen = protoc_gen