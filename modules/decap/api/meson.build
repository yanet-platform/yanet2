# controlplane dependecies
cp_dependencies = dependencies
cp_dependencies += lib_config_dp_dep

includes = include_directories('../dataplane')

sources = files(
    'controlplane.c',
)

lib_decap_cp = static_library(
    'decap_cp',
    sources,
    dependencies: cp_dependencies,
    include_directories: includes,
    install: false,
)

lib_decap_cp_dep = declare_dependency(
    link_with: lib_decap_cp,
)
