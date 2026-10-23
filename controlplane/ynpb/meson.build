ynpb_dir = meson.current_source_dir()
root_dir = meson.project_source_root()

# Proto files for the controlplane
ynpb_proto_files = [
    join_paths(ynpb_dir, 'v1', 'gateway.proto'),
    join_paths(ynpb_dir, 'v1', 'logging.proto'),
    join_paths(ynpb_dir, 'v1', 'inspect.proto'),
    join_paths(ynpb_dir, 'v1', 'function.proto'),
    join_paths(ynpb_dir, 'v1', 'pipeline.proto'),
    join_paths(ynpb_dir, 'v1', 'device.proto'),
    join_paths(ynpb_dir, 'v1', 'counters.proto'),
    join_paths(ynpb_dir, 'v1', 'auth.proto'),
    join_paths(ynpb_dir, 'v1', 'readiness.proto'),
    join_paths(ynpb_dir, 'v1', 'metrics.proto'),
    join_paths(ynpb_dir, 'v1', 'port.proto'),
    join_paths(ynpb_dir, 'v1', 'memory.proto'),
]

# Generate protobuf files
ynpb_gen = custom_target(
    'ynpb-protoc',
    output: 'ynpb.protoset',
    input: ynpb_proto_files,
    command: [
        protoc,
        '-I', root_dir,
        '--go_out=paths=source_relative:' + root_dir,
        '--go-grpc_out=paths=source_relative:' + root_dir,
        '--descriptor_set_out=@OUTPUT@',
        '@INPUT@',
    ],
    # protoc refuses --dependency_out with several inputs, so imports from
    # other targets are tracked through their descriptor sets instead.
    depends: [common_protoc_gen, common_readiness_protoc_gen],
    build_by_default: true,
)
go_proto_targets += ynpb_gen
