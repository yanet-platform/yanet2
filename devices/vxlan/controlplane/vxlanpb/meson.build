vxlan_protoc_gen = custom_target(
  'vxlan-protoc',
  output: 'vxlan.protoset',
  depfile: 'vxlan.protoset.d',
  input: meson.current_source_dir() / 'v1/vxlan.proto',
  command: go_protoc + ['--dependency_out=@DEPFILE@', '@INPUT@'],
  build_by_default: true,
)
go_proto_targets += vxlan_protoc_gen
