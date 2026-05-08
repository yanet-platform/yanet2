root_dir = meson.project_source_root()
proto_dir = meson.current_source_dir()
proto_files = [
    join_paths(proto_dir, 'route.proto'),
]

protoc_gen = custom_target(
    'route-protoc',
    output: [
        'route.pb.go',
        'route_grpc.pb.go',
    ],
    input: proto_files,
    command: [
        protoc,
        '-I', root_dir,
        '--go_out=paths=source_relative:' + root_dir,
        '--go-grpc_out=paths=source_relative:' + root_dir,
        '@INPUT@',
    ],
    build_by_default: true,
)
route_protoc_gen = protoc_gen
