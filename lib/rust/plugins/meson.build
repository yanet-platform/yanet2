# The dataplane plugins of the fixture modules, alone in this directory.
#
# The dataplane loader maps each lib<name>_dp.so of a directory it scans to the
# module <name>, so the Go tests are handed this directory and nothing else
# lives in it. The plugins build under their own profile: a no_std plugin
# cannot unwind, which the api archive needs.
rust_sdk_fixture_plugins = custom_target(
  'rust-sdk-fixture-plugins',
  output: ['libfixture_dp.so', 'libtally_dp.so', 'libfib_dp.so', 'libfirewall_dp.so', 'libfirewall4_dp.so'],
  command: [
    'sh', '-c',
    'set -eu; "$1" build --profile plugin --locked --manifest-path "$2" ' +
      '-p yanet-sdk-fixture --features plugin --examples --target "$3"; ' +
      'dir="$CARGO_TARGET_DIR/$3/plugin/examples"; shift 3; ' +
      'for plugin in "$@"; do cp -p "$dir/${plugin##*/}" "$plugin"; done',
    'sh', cargo, rust_manifest, rust_triple, '@OUTPUT@',
  ],
  env: rust_env,
  build_by_default: false,
  build_always_stale: true,
)
