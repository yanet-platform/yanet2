proto_dir = join_paths(meson.current_source_dir(), 'v1')
root_dir = meson.project_source_root()
proto_files = [join_paths(proto_dir, 'operator.proto')]

protoc_gen = custom_target(
    'pipeline-operator-protoc',
    output: 'pipeline-operator.protoset',
    depfile: 'pipeline-operator.protoset.d',
    input: proto_files,
    command: [
        protoc,
        '-I', proto_dir,
        '-I', root_dir,
        '--go_out=paths=source_relative:' + proto_dir,
        '--go-grpc_out=paths=source_relative:' + proto_dir,
        '--descriptor_set_out=@OUTPUT@',
        '--dependency_out=@DEPFILE@',
        '@INPUT@',
    ],
    build_by_default: true,
)
go_proto_targets += protoc_gen
pipeline_operator_protoc_gen = protoc_gen

readiness_proto_files = [
    join_paths(proto_dir, 'readiness.proto'),
]

pipeline_operator_readiness_protoc_gen = custom_target(
    'pipeline-operator-readiness-protoc',
    output: 'pipeline-operator-readiness.protoset',
    depfile: 'pipeline-operator-readiness.protoset.d',
    input: readiness_proto_files,
    command: [
        protoc,
        '-I', root_dir,
        '--go_out=paths=source_relative:' + root_dir,
        '--go-grpc_out=paths=source_relative:' + root_dir,
        '--descriptor_set_out=@OUTPUT@',
        '--dependency_out=@DEPFILE@',
        '@INPUT@',
    ],
    build_by_default: true,
)
go_proto_targets += pipeline_operator_readiness_protoc_gen
