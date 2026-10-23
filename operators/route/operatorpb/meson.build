proto_dir = join_paths(meson.current_source_dir(), 'v1')
root_dir = meson.project_source_root()
proto_files = [
    join_paths(proto_dir, 'operator.proto'),
    join_paths(proto_dir, 'route.proto'),
    join_paths(proto_dir, 'neighbour.proto'),
]

protoc_gen = custom_target(
    'route-operator-protoc',
    output: 'route-operator.protoset',
    input: proto_files,
    command: [
        protoc,
        '-I', proto_dir,
        '-I', root_dir,
        '--go_out=paths=source_relative:' + proto_dir,
        '--go-grpc_out=paths=source_relative:' + proto_dir,
        '--descriptor_set_out=@OUTPUT@',
        '@INPUT@',
    ],
    # protoc refuses --dependency_out with several inputs, so imports from
    # other targets are tracked through their descriptor sets instead.
    depends: [common_protoc_gen],
    build_by_default: true,
)
go_proto_targets += protoc_gen
route_operator_protoc_gen = protoc_gen

# Readiness proto imports common/readinesspb, so it must be compiled with
# -I root_dir only, using paths=source_relative:root_dir so that the generated
# source annotation embeds the full repo-relative path. A separate target
# prevents the bare-basename collision that would occur if readiness.proto were
# compiled under -I proto_dir alongside the common/readinesspb package.
readiness_proto_files = [
    join_paths(proto_dir, 'readiness.proto'),
]

route_operator_readiness_protoc_gen = custom_target(
    'route-operator-readiness-protoc',
    output: 'route-operator-readiness.protoset',
    depfile: 'route-operator-readiness.protoset.d',
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
go_proto_targets += route_operator_readiness_protoc_gen
