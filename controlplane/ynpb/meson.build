ynpb_dir = meson.current_source_dir()

# Proto files for the controlplane
ynpb_proto_files = [
    join_paths(ynpb_dir, 'gateway.proto'),
    join_paths(ynpb_dir, 'logging.proto'),
    join_paths(ynpb_dir, 'inspect.proto'),
    join_paths(ynpb_dir, 'pipeline.proto'),
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
        'pipeline.pb.go',
        'pipeline_grpc.pb.go',
    ],
    input: ynpb_proto_files,
    command: [
        protoc,
        '-I', ynpb_dir,
        '--go_out=paths=source_relative:' + ynpb_dir,
        '--go-grpc_out=paths=source_relative:' + ynpb_dir,
        '@INPUT@',
    ],
    build_by_default: true,
)

