# The dataplane test harness as a process, for the native control plane's
# tests.
#
# The Go client in controlplane/native/nativetest starts it on storage it
# creates and drives it over a pipe, so those tests need no cgo. The helper
# is a cgo Go program over the harness's binding, test-only: built here,
# never installed, and only where the native layouts are generated. An
# AddressSanitizer build instruments its Go side as well, as the asan suite
# builds the tests that start it.
if yanet_go_binaries and native_layout_enabled
  native_dataplane_helper = custom_target(
    'dataplane-helper',
    output: 'dataplane-helper',
    command: [
      go,
      'build',
      '-buildvcs=false',
    ] + (b_sanitize.contains('address') ? ['-asan'] : []) + [
      '-o', '@OUTPUT@',
      meson.project_source_root() / 'bindings/go/dataplane_ut/cmd/dataplane-helper',
    ],
    env: yanet_go_env,
    build_by_default: true,
    # Go tracks the helper's sources itself.
    build_always_stale: true,
    # The cgo stamps take the native libraries the binding links as inputs.
    depends: go_proto_targets + cgo_test_deps,
  )
  native_test_deps += native_dataplane_helper
endif
