# Native clang-tidy reports

Run `make lint/clang-tidy` from a configured checkout. This is an opt-in,
report-only trial: findings remain **unreviewed**, and no checker is admitted to
the mandatory gate or CI. A complete report exits zero even with findings.
Operational errors or incomplete coverage exit nonzero.

Prerequisites are Python 3.9+ (standard library only), Git, Meson, the configured C
compiler and **clang-tidy 19**. Configure and prepare a real build first, including
generated headers and initialized submodules. The runner refreshes Meson metadata
with `meson setup --reconfigure`; it does not build missing generated inputs.

The command includes every first-party C context registered by the configured
Meson targets: production code, tests and helpers, benchmarks, registered fuzz
reproducers and configured `extra_modules`, including symlinked module roots.
It reconciles the compiler and ordered flags for every source/target occurrence;
two variants of one source require two database entries. Relative include paths
are normalized only for matching. Replay preserves the actual compiler arguments,
working directories, assertions, defines and generated include paths.

Third-party targets, Rust (including `lib/rust` C), cgo link probes and standalone
uprobe/BPF tools are excluded. This command captures **no cgo compilation**.
Unregistered C sources, inactive preprocessor branches and other build
configurations have no coverage claim. Included first-party/generated headers
retain diagnostics and context attribution, including first-party headers marked
as system headers. Actual analysis includes come from dependency files emitted by
the same clang-tidy front end, including forced and compiler-conditional includes.
The configured compiler's dependency scan is retained separately as input evidence;
its include set is not labeled as Clang analysis coverage. Tracked and configured
extra-module
headers that no selected context reaches are listed as gaps, with no fabricated
standalone commands. Third-party warnings are filtered; parse errors still fail.
Filters cover absolute and compiler-working-directory-relative diagnostic paths.
Unresolved ownership of a registered first-party C source fails coverage.

`lint/clang-tidy-report.json` lists the exact trial checks and pins LLVM major 19.
The provisional 22-check selection also enables
`clang-analyzer-unix.StdCLibraryFunctions` for the demonstrated stdio model
dependency. That checker can emit its own diagnostics, which also remain
unreviewed. This profile changes no admission decision. The existing manual
`.clang-tidy` naming policy remains in place; the report overrides its check set
explicitly and verifies configuration and requested check names. Automatically
enabled analyzer dependencies are recorded separately from the requested profile.

Each invocation creates a fresh directory in `build/clang-tidy-reports/` and
prints its location. Invalid or foreign build paths retain error evidence under
the requested source root's `.agent-state/clang-tidy-reports/`, without modifying
the foreign build. Each report directory contains:

- `report.md`: revision with local worktree changes, coverage, exclusions, header
  gaps, grouped unreviewed findings and timing.
- `report.json`: revision and local changes, configured options, versions, exact
  profile, contexts, preparation/compiler/replay operation metadata and all finding
  associations. Operations retain command, working directory, return code and duration.
- `metadata/`, `profile.json`, `clang-tidy.yaml`: the metadata/configuration used.
- `preparation/`: refresh, version, validation and effective configuration logs.
- `contexts/<id>/`: the single-command database, native dependency stdout and
  separate stderr logs, the actual Clang `analysis.d` include inventory and full
  clang-tidy output, including analyzer path notes.

Grouping uses checker, normalized location and message. Every occurrence retains
its source, target, context and trace; grouping never discards distinct paths.
Reports and logs may contain paths and findings from configured private modules;
keep them local and review before sharing.

Concurrency defaults to eight workers (bounded by CPU count for direct script
invocations); every preparation, dependency and analysis command has a 120-second
timeout. Reports record dependency scans, preparation (including those scans),
analysis-only wall time, total wall time and per-command durations. Both process
timeouts and interruption retain an incomplete
report and logs; SIGKILL or power loss leaves the initial incomplete report.

Use a different configured build or tool path through
`make lint/clang-tidy CLANG_TIDY_BUILD=build-debug CLANG_TIDY=/path/to/clang-tidy-19
CLANG_TIDY_JOBS=4`. For timeout/output control, invoke the same runner directly:

```bash
python3 -B scripts/clang_tidy.py --build-dir build --jobs 4 --timeout 180 \
  --output /tmp/native-tidy-report
python3 -B -m unittest discover -s scripts -p test_clang_tidy.py
```

The real-tool controls also require GCC for the compiler-conditional include case.

An explicit output directory must not already exist. Missing tools, invalid
configuration/profile, unmatched or missing compile contexts, missing includes,
compiler/parser errors and timeouts fail the run. Findings alone never fail this
trial. Existing native tests, sanitizers and other verification remain necessary.
