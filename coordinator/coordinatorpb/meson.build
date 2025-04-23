proto_dir = meson.current_source_dir()
proto_files = [
    join_paths(proto_dir, 'registry.proto'),
    join_paths(proto_dir, 'module.proto'),
]

protoc_gen = custom_target(
    'coordinator-protoc',
    output: [
        'registry.pb.go', 
        'registry_grpc.pb.go',
        'module.pb.go',
        'module_grpc.pb.go',
    ],
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
coordinator_protoc_gen = protoc_gen
