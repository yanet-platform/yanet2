proto_dir = join_paths(meson.current_source_dir(), 'v1')
root_dir = meson.project_source_root()
proto_files = [
    join_paths(proto_dir, 'operator.proto'),
    join_paths(proto_dir, 'route.proto'),
    join_paths(proto_dir, 'neighbour.proto'),
]

protoc_gen = custom_target(
    'route-operator-protoc',
    output: [
        'operator.pb.go',
        'operator_grpc.pb.go',
        'route.pb.go',
        'route_grpc.pb.go',
        'neighbour.pb.go',
        'neighbour_grpc.pb.go',
    ],
    input: proto_files,
    command: [
        protoc,
        '-I', proto_dir,
        '-I', root_dir,
        '--go_out=paths=source_relative:' + proto_dir,
        '--go-grpc_out=paths=source_relative:' + proto_dir,
        '@INPUT@',
    ],
    build_by_default: true,
)
route_operator_protoc_gen = protoc_gen

# Readiness proto imports common/readinesspb, so it must be compiled with
# -I root_dir only, using paths=source_relative:root_dir so that the generated
# source annotation embeds the full repo-relative path. A separate target
# prevents the bare-basename collision that would occur if readiness.proto were
# compiled under -I proto_dir alongside the common/readinesspb package.
readiness_proto_files = [
    join_paths(proto_dir, 'readiness.proto'),
]

readiness_protoc_gen = custom_target(
    'route-operator-readiness-protoc',
    output: [
        'readiness.pb.go',
        'readiness_grpc.pb.go',
    ],
    input: readiness_proto_files,
    command: [
        protoc,
        '-I', root_dir,
        '--go_out=paths=source_relative:' + root_dir,
        '--go-grpc_out=paths=source_relative:' + root_dir,
        '@INPUT@',
    ],
    build_by_default: true,
)
route_operator_readiness_protoc_gen = readiness_protoc_gen
