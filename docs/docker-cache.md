# Docker cache

Docker development builds (`just dbuild`, `dtest`, etc.) share host caches with containers:

- Go caches and ccache live under `DOCKER_CACHE_DIR` (default `/tmp`): `gomodcache/`, `gocache/`, and `ccache/` are bind-mounted into the container at `/tmp/gomodcache`, `/tmp/gocache`, and `/ccache`.
- ccache uses its `ccache/` subdirectory with a 10 GB limit (`CCACHE_DIR=/ccache`, `CCACHE_MAXSIZE=10G` in the development image).
- `TAG` selects the development image, including nested Docker recipes.

## ccache discovery

- Fresh Meson setup automatically discovers ccache when no compiler is specified.
- Explicit `CC` overrides must include the `ccache` prefix, for example `CC='ccache clang'`.

## Fuzzing exception

`just fuzz` hardcodes `CC=clang CXX=clang++` in the recipe, and `just dfuzz` runs that same recipe in the container, so this cache integration does not automatically apply to fuzz builds. Prefixing the invocation (for example `CC='ccache clang' just dfuzz`) does not override the hardcoding.
