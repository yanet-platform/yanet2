# Docker cache

Docker development builds (`just dbuild`, `dtest`, etc.) share host caches with containers:

- Go caches and ccache live under `DOCKER_CACHE_DIR` (default `/tmp`): `gomodcache/`, `gocache/`, and `ccache/` are bind-mounted into the container at `/tmp/gomodcache`, `/tmp/gocache`, and `/ccache`.
- ccache uses its `ccache/` subdirectory with a 10 GB limit (`CCACHE_DIR=/ccache`, `CCACHE_MAXSIZE=10G` in the development image).
- `TAG` selects the development image, including nested Docker recipes.
- `DOCKER_CACHE_DIR` must be a nonempty absolute host path.
- If your development image predates ccache support, rebuild it with `just dbuild-cnt` using the same `TAG` as your build or test command.

## ccache discovery

- Fresh Meson setup automatically discovers ccache when no compiler is specified.
- Set explicit compiler overrides inside the container, for example `CC='ccache clang' CXX='ccache clang++' just _ccache_setup` from `just dshell`. The Docker runner does not forward the host's `CC` or `CXX` environment variables.
- `just dbuild` and `just dtest` check the generated Ninja C/C++ compilation commands before compiling. Installing ccache does not change an already configured compiler: an uncached build fails with a request to manually reconfigure or recreate it. The check never deletes or reconfigures the build and never replaces your compiler choice. Use `CXX='ccache clang++'` for an explicit C++ compiler.
- The shared runner creates all three host cache directories before starting Docker. The isolated two-container probe also checks their host ownership; it does not precreate them itself.
- The shared runner sets `CCACHE_UMASK=000` for new cache entries so the host user can remove root-written entries. It does not repair old root-owned caches. Cache contents are world-writable: use a private cache root (e.g. a mode-0700 directory) or share it only with trusted users.
- Masquerade compiler paths (e.g. `CC=/usr/lib/ccache/cc`) are accepted as cached by the check.

## Fuzzing exception

`just fuzz` hardcodes `CC=clang CXX=clang++` in the recipe, and `just dfuzz` runs that same recipe in the container, so this cache integration does not automatically apply to fuzz builds. Prefixing the invocation (for example `CC='ccache clang' just dfuzz`) does not override the hardcoding.
