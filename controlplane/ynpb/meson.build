ynpb_dir = meson.current_source_dir()
root_dir = meson.project_source_root()

# Proto files for the controlplane
ynpb_proto_files = [
    join_paths(ynpb_dir, 'v1', 'gateway.proto'),
    join_paths(ynpb_dir, 'v1', 'logging.proto'),
    join_paths(ynpb_dir, 'v1', 'inspect.proto'),
    join_paths(ynpb_dir, 'v1', 'module.proto'),
    join_paths(ynpb_dir, 'v1', 'function.proto'),
    join_paths(ynpb_dir, 'v1', 'pipeline.proto'),
    join_paths(ynpb_dir, 'v1', 'device.proto'),
    join_paths(ynpb_dir, 'v1', 'counters.proto'),
    join_paths(ynpb_dir, 'v1', 'auth.proto'),
]

# Generate protobuf files
ynpb_gen = custom_target(
    'ynpb-protoc',
    output: [
        'gateway.pb.go',
        'gateway_grpc.pb.go',
        'logging.pb.go',
        'logging_grpc.pb.go',
        'inspect.pb.go',
        'inspect_grpc.pb.go',
        'module.pb.go',
        'module_grpc.pb.go',
        'function.pb.go',
        'function_grpc.pb.go',
        'pipeline.pb.go',
        'pipeline_grpc.pb.go',
        'device.pb.go',
        'device_grpc.pb.go',
        'counters.pb.go',
        'counters_grpc.pb.go',
        'auth.pb.go',
        'auth_grpc.pb.go',
    ],
    input: ynpb_proto_files,
    command: [
        protoc,
        '-I', root_dir,
        '--go_out=paths=source_relative:' + root_dir,
        '--go-grpc_out=paths=source_relative:' + root_dir,
        '@INPUT@',
    ],
    build_by_default: true,
)
