proto_dir = meson.current_source_dir()
root_dir = meson.project_source_root()
proto_files = [
    join_paths(proto_dir, 'operator.proto'),
    join_paths(proto_dir, 'route.proto'),
    join_paths(proto_dir, 'neighbour.proto'),
]

protoc_gen = custom_target(
    'route-operator-protoc',
    output: [
        'operator.pb.go',
        'operator_grpc.pb.go',
        'route.pb.go',
        'route_grpc.pb.go',
        'neighbour.pb.go',
        'neighbour_grpc.pb.go',
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
route_operator_protoc_gen = protoc_gen
