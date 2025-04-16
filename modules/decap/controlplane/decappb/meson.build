proto_dir = meson.current_source_dir()
proto_files = [join_paths(proto_dir, 'decap.proto')]

protoc_gen = custom_target(
    'decap-protoc',
    output: ['decap.pb.go', 'decap_grpc.pb.go'],
    input: proto_files,
    command: [
        protoc,
        '-I', meson.current_source_dir(),
        '--go_out=paths=source_relative:' + meson.current_source_dir(),
        '--go-grpc_out=paths=source_relative:' + meson.current_source_dir(),
        '@INPUT@',
    ],
    build_by_default: true,
)
decap_protoc_gen = protoc_gen
