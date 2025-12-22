root_dir = meson.project_source_root()
proto_files = [
    join_paths(root_dir, 'modules/balancer/controlplane/balancerpb/balancer.proto'),
    join_paths(root_dir, 'modules/balancer/controlplane/balancerpb/stats.proto'),
    join_paths(root_dir, 'modules/balancer/controlplane/balancerpb/info.proto'),
    join_paths(root_dir, 'modules/balancer/controlplane/balancerpb/module.proto'),
]

protoc_gen = custom_target(
    'balancer-protoc',
    output: [
        'balancer.pb.go',
        'balancer_grpc.pb.go',
        'stats.pb.go',
        'info.pb.go',
        'module.pb.go',
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
balancer_protoc_gen = protoc_gen
