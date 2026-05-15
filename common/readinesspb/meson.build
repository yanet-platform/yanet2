readiness_proto_dir = meson.current_source_dir()
root_dir = meson.project_source_root()

readiness_proto_files = [
    join_paths(readiness_proto_dir, 'readiness.proto'),
]

readiness_protoc_gen = custom_target(
    'readiness-protoc',
    output: [
        'readiness.pb.go',
        'readiness_grpc.pb.go',
    ],
    input: readiness_proto_files,
    command: [
        protoc,
        '-I', readiness_proto_dir,
        '-I', root_dir,
        '--go_out=paths=source_relative:' + readiness_proto_dir,
        '--go-grpc_out=paths=source_relative:' + readiness_proto_dir,
        '@INPUT@',
    ],
    build_by_default: true,
)
