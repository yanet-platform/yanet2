root_dir = meson.project_source_root()
proto_dir = join_paths(meson.current_source_dir(), 'v1')
proto_files = [
    join_paths(proto_dir, 'route.proto'),
]

protoc_gen = custom_target(
    'route-protoc',
    output: 'route.protoset',
    depfile: 'route.protoset.d',
    input: proto_files,
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
go_proto_targets += protoc_gen
route_protoc_gen = protoc_gen
