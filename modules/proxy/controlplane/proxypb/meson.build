proto_dir = meson.current_source_dir()
root_dir = meson.project_source_root()
proto_files = [join_paths(proto_dir, 'proxy.proto')]

protoc_gen = custom_target(
    'proxy-protoc',
    output: ['proxy.pb.go', 'proxy_grpc.pb.go'],
    input: proto_files,
    command: [
        protoc,
        '-I', proto_dir,
        '-I', root_dir,
        '--go_out=paths=source_relative:' + proto_dir,
        '--go-grpc_out=paths=source_relative:' + proto_dir,
        '@INPUT@',
    ],
    depends: [common_protoc_gen],
    build_by_default: true,
)
proxy_protoc_gen = protoc_gen
