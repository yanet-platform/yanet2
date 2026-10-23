proto_dir = join_paths(meson.current_source_dir(), 'v1')
root_dir = meson.project_source_root()
proto_files = [join_paths(proto_dir, 'mirror.proto')]

protoc_gen = custom_target(
    'mirror-protoc',
    output: 'mirror.protoset',
    depfile: 'mirror.protoset.d',
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
mirror_protoc_gen = protoc_gen
