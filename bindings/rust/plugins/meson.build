# The dataplane plugins of the fixture modules, alone in this directory.
#
# The dataplane loader maps each lib<name>_dp.so of a directory it scans to the
# module <name>, and finds an object type of a loaded plugin by its type name,
# so the Go tests are handed this directory and nothing else lives in it. The
# plugins build under their own profile: a no_std plugin cannot unwind, which
# the api archive needs.

# With sdk_rust_llvm_bindir, the plugins link with cross-language LTO: the
# shims are ThinLTO bitcode of that clang, the Rust code bitcode for the CPU
# the C sources are built for (the inliner refuses a C function of other
# target features), and that lld links both, so the shim lookups inline into
# the handlers. Only the plugins: the api archive goes through a relocatable
# link and cgo, which take machine code. Their own cargo target directory
# keeps the two sets of flags from rebuilding each other.
rust_plugin_env = rust_env
llvm_bindir = get_option('sdk_rust_llvm_bindir')
if llvm_bindir != ''
  if host_machine.cpu_family() != 'x86_64'
    error('sdk_rust_llvm_bindir: cross-language LTO is wired for x86_64 only')
  endif
  if yanet_sanitizers.length() > 0
    error('sdk_rust_llvm_bindir: no cross-language LTO in a sanitized build')
  endif
  rust_plugin_env += {
    'CARGO_TARGET_DIR': rust_target_dir / 'lto',
    'YANET_SYS_LTO_CC': llvm_bindir / 'clang',
    'YANET_SYS_LTO_AR': llvm_bindir / 'llvm-ar',
    'RUSTFLAGS': ' '.join([
      '-Clinker-plugin-lto',
      '-Ctarget-cpu=' + get_option('cpu_arch'),
      '-Clinker=' + (llvm_bindir / 'clang'),
      '-Clink-arg=--ld-path=' + (llvm_bindir / 'ld.lld'),
    ]),
  }
endif

rust_sdk_fixture_plugins = custom_target(
  'rust-sdk-fixture-plugins',
  output: [
    'libfixture_dp.so',
    'libtally_dp.so',
    'libfib_dp.so',
    'libassign_dp.so',
    'libfixture_state_dp.so',
    'libstash_dp.so',
  ],
  command: [
    'sh', '-c',
    'set -eu; "$1" build --profile plugin --locked --manifest-path "$2" ' +
      '-p yanet-sdk-fixture --features plugin --examples --target "$3"; ' +
      'dir="$CARGO_TARGET_DIR/$3/plugin/examples"; shift 3; ' +
      'for plugin in "$@"; do cp -p "$dir/${plugin##*/}" "$plugin"; done',
    'sh', cargo, rust_manifest, rust_triple, '@OUTPUT@',
  ],
  env: rust_plugin_env,
  build_by_default: false,
  build_always_stale: true,
)
