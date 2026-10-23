readiness_proto_dir = meson.current_source_dir()
root_dir = meson.project_source_root()

readiness_proto_files = [
    join_paths(readiness_proto_dir, 'v1', 'readiness.proto'),
]

common_readiness_protoc_gen = custom_target(
    'readiness-protoc',
    output: 'readiness.protoset',
    depfile: 'readiness.protoset.d',
    input: readiness_proto_files,
    command: [
        protoc,
        '-I', root_dir,
        '--go_out=paths=source_relative:' + root_dir,
        '--descriptor_set_out=@OUTPUT@',
        '--dependency_out=@DEPFILE@',
        '@INPUT@',
    ],
    build_by_default: true,
)
go_proto_targets += common_readiness_protoc_gen
