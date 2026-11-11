pipeline_operator_protoc_gen = custom_target(
  'pipeline-operator-protoc',
  output: 'pipeline-operator.protoset',
  depfile: 'pipeline-operator.protoset.d',
  input: meson.current_source_dir() / 'v1/operator.proto',
  command: go_protoc + ['--dependency_out=@DEPFILE@', '@INPUT@'],
  build_by_default: true,
)
go_proto_targets += pipeline_operator_protoc_gen

pipeline_operator_readiness_protoc_gen = custom_target(
  'pipeline-operator-readiness-protoc',
  output: 'pipeline-operator-readiness.protoset',
  depfile: 'pipeline-operator-readiness.protoset.d',
  input: meson.current_source_dir() / 'v1/readiness.proto',
  command: go_protoc + ['--dependency_out=@DEPFILE@', '@INPUT@'],
  build_by_default: true,
)
go_proto_targets += pipeline_operator_readiness_protoc_gen
