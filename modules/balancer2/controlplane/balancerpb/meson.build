root_dir = meson.project_source_root()
proto_files = [
    join_paths(root_dir, 'modules/balancer2/controlplane/balancerpb/v1/balancer.proto'),
    join_paths(root_dir, 'modules/balancer2/controlplane/balancerpb/v1/config.proto'),
    join_paths(root_dir, 'modules/balancer2/controlplane/balancerpb/v1/filter.proto'),
    join_paths(root_dir, 'modules/balancer2/controlplane/balancerpb/v1/state.proto'),
]

protoc_gen = custom_target(
    'balancer-protoc',
    output: [
        'balancer.pb.go',
        'balancer_grpc.pb.go',
        'config.pb.go',
        'filter.pb.go',
        'state.pb.go',
    ],
    input: proto_files,
    command: [
        protoc,
        '-I', root_dir,
        '--experimental_allow_proto3_optional',
        '--go_out=paths=source_relative:' + root_dir,
        '--go-grpc_out=paths=source_relative:' + root_dir,
        '@INPUT@',
    ],
    build_by_default: true,
)
balancer_protoc_gen = protoc_gen
