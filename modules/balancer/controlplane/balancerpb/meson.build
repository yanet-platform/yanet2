proto_dir = meson.current_source_dir()
root_dir = meson.project_source_root()
proto_files = [join_paths(proto_dir, 'balancer.proto')]

protoc_gen = custom_target(
    'balancer-protoc',
    output: ['balancer.pb.go', 'balancer_grpc.pb.go'],
    input: proto_files,
    command: [
        protoc,
        '-I', proto_dir,
        '-I', root_dir,
        '--go_out=paths=source_relative:' + proto_dir,
        '--go-grpc_out=paths=source_relative:' + proto_dir,
        '@INPUT@',
    ],
    depends: [common_protoc_gen, lib_balancer_cp],
    build_by_default: true,
)
balancer_protoc_gen = protoc_gen
