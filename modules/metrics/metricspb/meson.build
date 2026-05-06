proto_dir = meson.current_source_dir()
common_dir = join_paths(meson.source_root(), 'common')
proto_files = [join_paths(proto_dir, 'metrics.proto')]

protoc_gen = custom_target(
    'metrics-protoc',
    output: ['metrics.pb.go', 'metrics_grpc.pb.go'],
    input: proto_files,
    command: [
        protoc,
        '-I', proto_dir,
        '-I', common_dir,
        '--go_out=paths=source_relative:' + proto_dir,
        '--go-grpc_out=paths=source_relative:' + proto_dir,
        '@INPUT@',
    ],
    build_by_default: true,
)
metrics_protoc_gen = protoc_gen