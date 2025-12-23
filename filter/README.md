# Filter module

Filter provides packet classification trees used by components across the project.
It includes a header-only query API and a compiler library.

## Rules
Rules are described in [rule.h](rule.h) and carried in struct filter_rule:
- L3 (IPv4/IPv6) nets for src/dst, transport (proto/ports/TCP flags), device/VLAN
- 32-bit action layout: [31..16] category mask, [15] non-terminate, [14..0] user
See the detailed layout and helpers in [rule.h](rule.h).


## Libraries

- Query
  - Interface: filter/query.h
  - Macros: FILTER_QUERY_DECLARE, FILTER_QUERY
  - Functions: filter_actions_with_category

- Compiler
  - Interface: filter/compiler.h (+ implementation under filter/compiler/*.c)
  - Macros: FILTER_COMPILER_DECLARE, FILTER_INIT, FILTER_FREE

## Files

- Public interfaces:
  - filter/rule.h — rule data types and action encoding (inputs to compiler, visible to query)
  - filter/compiler.h — build (compile) filter for a given signature
  - filter/query.h — query filter and post-process actions
  - filter/filter.h — core types (struct filter, vertices, tables, registries)

See inline docs in compiler.h, query.h and rule.h.

## Quick start

1) Declare attribute signature (must be consistent between compiler and query):

```c
// Choose attribute order explicitly
FILTER_COMPILER_DECLARE(my_sig, port_src, net4_dst);
FILTER_QUERY_DECLARE(my_sig, port_src, net4_dst);
```

2) Build a rule:

```c
// one src port range [1000..2000], one dst net 192.168.0.0/24
struct filter_port_range src_pr = {1000, 2000};
struct net4 dst_net = {.addr = {192,168,0,0}, .mask = {255,255,255,0}};

struct filter_rule r = {0};
r.net4.dst_count = 1;
r.net4.dsts = &dst_net;

r.transport.src_count = 1;
r.transport.srcs = &src_pr;

r.action = 42; // user action (lower 15 bits), terminal by default
```

3) Initialize filter

```c
struct filter f;
int rc = FILTER_INIT(&f, my_sig, &r, 1, &memory_context);
assert(rc == 0);
```

4) Query

```c
uint32_t *actions;
uint32_t count;
FILTER_QUERY(&f, my_sig, &packet, &actions, &count);
// actions points into filter-owned memory; do not free
```

5) Free

```c
FILTER_FREE(&f, my_sig);
```

## API reference

- Build and free
  - `FILTER_INIT(filter, tag, rules, rule_count, ctx)`
    - Compiles rules into the filter using attribute signature `tag`
    - Returns 0 on success, negative on error
  - `FILTER_FREE(filter, tag)`
    - Releases resources allocated by FILTER_INIT

- Query and post-processing
  - `FILTER_QUERY(filter_ptr, tag, packet_ptr, actions_out_ptr, count_out_ptr)`
    - Classifies packet, returns actions slice owned by the filter
  - `filter_actions_with_category(actions, count, category)`
    - In-place filter by category and stops on terminal action

See details in headers:
- Rules in [rule.h](rule.h)
- Build in [compiler.h](compiler.h)
- Query in [query.h](query.h)

## Notes

- Attribute order matters and is defined by your `FILTER_*_DECLARE` signatures. Use the same order in both DECLAREs (compiler and query).
- The classification result (`actions`, `count`) is a view into filter memory. Copy if you need to retain past next calls or destruction.
- Memory is managed via `struct memory_context`.


## Real-world usage

The project integrates the filter in two places with two libraries:
- Query (header-only):
  - Use for dataplane/runtime classification
  - Interface headers: [`filter/query.h`](filter/query.h) and [`filter/filter.h`](filter/filter.h)
  - Meson dependency: [`lib_filter_query_dep`](filter/meson.build:1) (include-only, no link objects)
- Compiler:
  - Use for controlplane to build the filter instance from rules
  - Interface: [`filter/compiler.h`](filter/compiler.h) (implementation under `filter/compiler/*.c`)
  - Meson target/dep: [`lib_filter_compiler`](filter/meson.build:16) / [`lib_filter_compiler_dep`](filter/meson.build:25)

### Meson integration

- Dataplane (query-only):
  - Add include dependency only:
    ```
    deps += [lib_filter_query_dep]
    ```
- Controlplane (needs compiler):
  - Add both include and link dependency to the static library:
    ```
    deps += [lib_filter_compiler_dep]
    ```

See the library declarations in [`filter/meson.build`](filter/meson.build:1).

### Typical flow in this codebase

1) Controlplane prepares rules and builds a filter:
   - Declare signature once with macros:
     - Compiler side: [`FILTER_COMPILER_DECLARE`](filter/compiler/declare.h) and then build via [`FILTER_INIT`](filter/compiler.h:28)
     - Query side (for consumers): [`FILTER_QUERY_DECLARE`](filter/query/attribute.h)
   - Persist or share the built filter (for example via shared memory as in shm tests)

2) Dataplane queries the filter:
   - Include query headers and declare the same signature order with [`FILTER_QUERY_DECLARE`](filter/query/attribute.h)
   - On packet path, call [`FILTER_QUERY`](filter/query.h:119) to obtain actions slice (owned by filter memory)

Minimal example (controlplane build):
```c
// signatures (order must match on both sides)
FILTER_COMPILER_DECLARE(my_sig, port_src, net4_dst);

// build rules and compile
struct filter f;
int rc = FILTER_INIT(&f, my_sig, rules, rules_count, &mctx);
assert(rc == 0);
```

Minimal example (dataplane query):
```c
FILTER_QUERY_DECLARE(my_sig, port_src, net4_dst);

uint32_t *actions;
uint32_t count;
FILTER_QUERY(&f, my_sig, &packet, &actions, &count);
// actions points to filter-owned memory
```

Notes:
- Attribute ordering in DECLARE macros defines the tree layout; keep the same order for both compiler and query sides.
- Query is header-only; compiler links implementation objects from `filter/compiler/*.c`.
- Actions memory is owned by the filter; copy if you need to retain it beyond the next update/free.

## Testing helpers (only for tests)

`filter/tests/helpers.h` provides a small rule builder surface used in unit tests:
- `builder_add_port_src_range`, `builder_add_port_dst_range`
- `builder_add_net4_src`, `builder_add_net4_dst`
- `builder_add_net6_src`, `builder_add_net6_dst`
- `builder_add_proto_range`, `builder_set_proto`
- `builder_set_vlan`
