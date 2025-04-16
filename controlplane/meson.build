ynpb_dir = join_paths(meson.current_source_dir(), 'ynpb')

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

custom_target(
    'yanet-controlplane',
    output: 'yanet-controlplane',
    command: [
        go,
        'build',
        '-o', '@OUTPUT@',
        join_paths(meson.current_source_dir(), 'cmd', 'yncp-director', 'main.go'),
    ],
    build_by_default: true,
    depends: [
        ynpb_gen,
        decap_protoc_gen,
        forward_protoc_gen,
        nat64_protoc_gen,
        route_protoc_gen,
    ],
    install: true,
    install_dir: get_option('bindir'),
)
