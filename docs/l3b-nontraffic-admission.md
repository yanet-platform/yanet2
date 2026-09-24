# L3B Nontraffic Admission

This record covers issue [#2721](https://github.com/yanet-platform/yanet2/issues/2721):
construction, discovery and bounded lifecycle of the existing L3B module and
linked object types. It does not qualify traffic, production trust or release
behaviour. An empty runtime is not evidence that enabled real servers are safe.

## Revision and Approval

- Base: `d0df58e37bb5b68a8c2e7bf96613e10f93efcc58`, freshly fetched on 2026-09-24.
- Merged prerequisite: [#2784](https://github.com/yanet-platform/yanet2/pull/2784),
  head `5b772e0f76a8af1b846eb1882c75890bcd376028`, merged at
  `2026-09-24T10:56:03Z` as `d0df58e37bb5b68a8c2e7bf96613e10f93efcc58`.
  Live GitHub state and the fetched main baseline confirm the merge.
- The user explicitly approved retention of the `modules/l3b` / `objects/l3b`
  split in the implementation session on 2026-09-24, before this completion edit.
  The same instruction assigned remaining deadline, cancellation, trust and
  release qualification to @moonug. This is session provenance, not an invented
  issue comment or GitHub review.
- No commit, push, GitHub write, deployment or traffic admission accompanies this
  record. Fresh independent full review of the final candidate remains a gate.

## Layout Comparison

The reference is the existing decap module, with dscp, forward and route as
additional canonical layouts.

| Surface | Reference pattern | L3B candidate |
| --- | --- | --- |
| Module C API and Go bindings | `modules/<name>/api` and `bindings/go` | Same; module config bindings in `modules/l3b/bindings/go/cl3b` |
| Control plane | Config, constructor, backend, service and protobufs under `controlplane` | Same; one declared L3B gRPC service |
| Attachment | Embedded `ffi.AttachConfig`, `ffi.Attach`, owned `ffi.Attachment.Close` | Same in the merged #2784 baseline; flat YAML keys and 64 MiB default retained |
| Dataplane | `dataplane/config.h`, handler and exported constructor | Same; `new_module_l3b` resolved by the production module loader |
| Shared objects | No equivalent needed by decap | Approved split: `objects/l3b/api` and `bindings/go/cl3bobject`; shared virtual-service and session-table types |
| CLI | Co-located Rust CLI | `modules/l3b/cli`; CLI execution/packaging is outside this admission run |
| Tests | Co-located unit tests and module tests | Independent constructor tests plus a test-only file-backed shared-memory fixture |

The merged prerequisite consists of
`modules/l3b/controlplane/{cfg.go,mod.go,cfg_test.go,mod_test.go}`. Its config and
missing-file tests are preserved from main. The final diff contains no changes
to the first three files; only admission imports/tests are added to the existing
module test file. A fresh worktree from main received only the previously
reviewed admission delta, with no duplicate prerequisite changes. Both
constructor cleanup fixes remain necessary because they are absent from main.

## Capability Inventory

"Supported" below means supported by this bounded nontraffic evidence, not a
production service-level or traffic-support commitment.

| Classification | Surface | Evidence or boundary |
| --- | --- | --- |
| Supported | Real linked module/type discovery | Production `dp_load_module` and `dp_load_object` resolve the current executable's symbols; exactly `l3b`, `l3b_virtual_service`, `l3b_session_table` |
| Supported | Missing module/object names | Loader failure preserves both inventories; no synthetic registrations |
| Supported | Direct constructor | Real file attachment with unmodified default 64 MiB module arena |
| Supported | Independent production constructor | `NewDirector` constructs Bundle and Gateway with only L3B configured |
| Supported | Discovery and empty reads | Exactly one L3B in-process backend; empty service/config lists and NotFound for absent named service/session reads |
| Supported | Bounded lifecycle and errors | Joined Run cancellation, already-canceled startup, occupied listener, missing file, invalid instance and partial-constructor mapping release |
| Supported | Shared attachment | Merged #2784 baseline; config and missing-file regression tests rerun |
| Transitional | File-backed Linux fixture | 4 MiB dataplane storage plus 128 MiB control-plane storage, including arena/bootstrap overhead; no production default changes |
| Transitional | Arena ownership | Tests release complete mappings/files; this does not prove arena reclamation by agent detach |
| Excluded | Active dataplane | No workers, packet loop, enabled reals, scheduler activation, live virtual-service/session-table instance or module-config publication |
| Excluded | External-module registration | L3B uses in-process registration; generic external-session tests are reuse evidence only |
| Excluded | Release qualification | No QEMU, Lab, packet-path, deferred-free/grace-period or deployment claims |

Object constructors here allocate only inert type descriptors, which the loader
copies and frees. They do not construct live virtual services or session tables.
The helper is under `common/go/testutils/shm`; production loaders,
dataplane/parser code and functional/dataplane_ut harnesses are unchanged.
The Go FFI attachment boundary first acquires readiness of instance zero, then
rejects an instance index outside its published count before calling the unsafe
C attachment path. Unpublished storage fails immediately without waiting.
The public method signature and successful attachment/cleanup path are unchanged.

## Review Correction

Full review of patch
`2d66e499873b33f4bfc31cf7d880e86177695a0a39aa717c81508560a3a60372`
completed all four layers but returned REQUEST_CHANGES for F1. The existing
invalid-instance test could reach a C read beyond its one-instance mapping;
previous passing tests and Go race instrumentation did not prove that safe.

The correction in `controlplane/ffi/shm.go` acquires the first instance's readiness
before the existing count API reads its header, without traversing the requested
index. This orders the count and layout reads after initialization publication;
the C attachment still checks readiness of the requested instance. The Go comparison
returns an attributable attachment error before allocating the C name or
calling the C attachment routine. No C implementation or storage sizing changes
are needed. `controlplane/ffi/shm_test.go` exercises the public method
with the last valid index (zero), the first invalid index (one) and MaxUint32,
and verifies that rejected attachments leave the agent inventory unchanged.
The existing L3B invalid-instance regression remains intact.

A later review added the readiness acquire and a fail-fast regression on a
zero-filled mapping for index zero and MaxUint32. This test verifies rejection
without traversal, not an ARM weak-memory reproduction or the fixed 60-second
readiness timeout. Subsequent gate results and review identities are recorded in
the publication record; the measured run below is retained as historical evidence.

The unguarded out-of-bounds path was not deliberately executed again. The
source establishes the unchecked traversal; the new tests pin the range error
and valid boundary on real storage. No C sanitizer coverage is claimed.

## Applicability and Owners

| Obligation | Applicability and status | Remaining owner |
| --- | --- | --- |
| Run cancellation and Close | Applicable to this empty runtime; executed by both constructor routes | @moonug for qualification beyond this slice |
| Generic proxy cancellation/deadline | Reused Gateway tests; not evidence of cancellation inside L3B backend work | @moonug |
| Fixed 60-second readiness timeout | Applicable to not-ready production memory; NOT RUN here because only ready storage is admitted and the production timeout is not changed | @moonug |
| Long-running backend cancellation | NOT PROVEN; backend methods have no context parameter and empty reads do not exercise long operations | @moonug |
| Trust and authorization | Generic Gateway TLS/auth tests are reuse evidence; the L3B route is local plaintext. Production trust acceptance NOT PROVEN | @moonug |
| Arena reclamation | Whole fixture unmapping is tested; detach reclamation NOT PROVEN and not inferred from mapping release | @moonug |
| Release-only lifetime/traffic checks | NOT RUN, outside the no-activation boundary; required before claiming release/traffic readiness | @moonug |

## Reproduction

Commands run from session CWD `/Users/moonug/projects/yanet/yanet2`. The user
authorized this isolated Docker alternative to the usual just recipes because
the candidate needs its own mount, real build and caches, and no traffic tests.

```sh
run_gate() {
  docker run --rm --platform linux/amd64 \
    --mount "type=bind,src=$PWD/.agent-state/worktrees/l3b-2721-final,dst=/yanet2" \
    -w /yanet2 \
    -e GOCACHE=/yanet2/.verify/f1-bounded/gocache \
    -e GOMODCACHE=/yanet2/.verify/f1-bounded/gomodcache \
    -e CARGO_TARGET_DIR=/yanet2/.verify/f1-bounded/cargo-target \
    -e 'CGO_CFLAGS=-O2 -g -march=nehalem -DYANET_CACHE_LINE_SIZE=64' \
    yanet2-remove-balancer2-tools:session bash .verify/f1-bounded/gates.sh "$1"
}
run_gate prepare
run_gate format
run_gate list
run_gate test
run_gate race
run_gate vet
run_gate build
run_gate artifacts
git -C .agent-state/worktrees/l3b-2721-final diff --check
```

Image ID: `sha256:1f3e0ef3c0271c36ec0432d1ac31fb4a1e930867927e544892b5ec7824d9a7d7`.
Platform: linux/amd64; Go 1.24.13, GCC 13.3.0, Meson 1.3.2, protoc 3.21.12.
Preparation uses `make proto-go`,
`meson setup --reconfigure build -Dcpu_arch=nehalem -Ddataplane_only=false -Dbuildtype=debugoptimized`,
then `ninja -C build -t clean` before rebuilding repository static libraries,
the Rust regex archive and libpcap. The build directory remains task-owned and
real; this correction uses new owned Go and Cargo caches.
No compiled test binary is executed during archive preparation.

Nontraffic execution uses `go test -p 4 -json -count=1 -timeout=180s` for
`./common/go/testutils/shm ./modules/l3b/controlplane/... ./controlplane/bundle
./controlplane/yncp ./controlplane/gateway ./controlplane/ffi`, with
`-skip 'Test_(Backend_|L3BService_|RingFromWeights_)|TestWorkerCounters'`.
The exclusions are two mutation/backend tests, seven mock-service tests and
three scheduler-ring tests plus four FFI synthetic-worker counter tests, not
passes. Race execution selects all new tests and
eight existing Gateway lifecycle/context tests and focused FFI lifecycle tests
with `-race -count=10`; JSON
assertions require every selected top-level test to pass exactly ten times.
Missing archives, missing loader symbols, failed checks and zero test execution
are failures, never skips or passes.

The exact focused race command inside the container is:

```sh
go test -p 4 -race -json -count=10 -timeout=180s \
  -run 'Test_(Storage_|Config_AttachmentYAML|NewL3BModule_MissingMemoryFile|L3BModule_Nontraffic|Director_L3B|NewBundle_L3B|Decode_L3BPresence|SharedMemory_AgentAttach_InstanceBounds|SharedMemory_Detach_|SharedMemory_Attach_MissingFile|AttachConfig_|DefaultAttachConfig_)|Test_Gateway_HostsRunAndCloseEveryService|Test_GatewayRun_ReadinessDrainLatchesLateReady|TestGateway_Run_(ShutsDownWithOpenStream|DrainsReadinessOnShutdown)|Test_InProcessServiceRunner_Run_ShutsDownWithOpenStream|TestGateway_ProxiedRPC_Client(Cancel|Disconnect|Deadline)PropagatesToBackendCtx' \
  ./common/go/testutils/shm ./modules/l3b/controlplane \
  ./controlplane/bundle ./controlplane/yncp ./controlplane/gateway ./controlplane/ffi
go vet ./common/go/testutils/shm ./modules/l3b/controlplane/... \
  ./controlplane/bundle ./controlplane/yncp ./controlplane/gateway ./controlplane/ffi
go build ./common/go/testutils/shm ./modules/l3b/controlplane/... \
  ./controlplane/bundle ./controlplane/yncp ./controlplane/gateway ./controlplane/ffi
go test -c -o .verify/f1-bounded/shm.test ./common/go/testutils/shm
nm -D --defined-only .verify/f1-bounded/shm.test
```

The fixture-only binary is an additional artifact demonstrating that retention
does not depend on the pre-existing heap-based backend test harness's linkage.
Only compilation and dynamic-symbol inspection are performed in this artifact
step; fixture tests already execute in the nontraffic and race gates.

## Results

F1 correction bounded run on 2026-09-24: PASS. Container
`yanet2-2721-f1-bounded` ran from `16:37:18Z` to `16:43:57Z` and exited 0.
The results below are newly executed evidence for the guarded candidate.

The first F1 run selected the FFI package too broadly and ran four existing
tests that construct synthetic worker records, outside the no-workers scope.
No packet loop or traffic test ran. That overbroad run is retained under
`.verify/f1/` and is not accepted as worker-free admission evidence. The corrected
selection explicitly excludes those four tests and writes separate evidence to
`.verify/f1-bounded/`; the history is not overwritten or silently relabeled.

| Gate | Result |
| --- | --- |
| Clean archive build and generated Go protobufs | PASS; includes libpcap and regex, no neighbouring archives |
| Test discovery | PASS; all selected tests present and sixteen excluded tests identified |
| Nontraffic tests | PASS; 133 top-level tests in seven packages, zero failures, runtime skips or excluded test executions |
| Repeated race gate | PASS; 27 distinct top-level tests, exactly ten passes each, zero failures/skips |
| Vet and Go build | PASS on fixture, L3B controlplane/protobufs, Bundle, Director, Gateway and FFI |
| Formatting and complete candidate diff check | PASS |
| Standalone fixture binary | PASS; all three real constructors present as defined dynamic symbols |

Nontraffic counts: fixture 2, L3B controlplane 4, L3B protobufs 12, Bundle 9,
Director 12, Gateway 67 and FFI 27. The race set includes ten admission tests,
two merged prerequisite tests, eight reused Gateway tests and seven FFI tests.
Each of the three new boundary subcases passed ten times, including the valid
last index and MaxUint32. Race instrumentation covers Go; no C sanitizer claim
is made. The FFI suite also ran its existing subprocess SIGBUS handling test;
that is not an AgentAttach out-of-bounds probe or sanitizer test.

Current evidence is retained under the final worktree's `.verify/f1-bounded/`:
`prepare.log`,
`list.txt`, `nontraffic.jsonl`, `race.jsonl`, `vet.log`, `build.log`, `gofmt.txt`,
`final-gates.log`, `dynamic-symbols.txt`, `archive-sha256.txt` and
`evidence-sha256.txt`. `gates.sh` retains exact commands.
`.verify/f1/manifest.sh` generates the complete ten-file `full.patch` in
`.verify/f1-bounded/`, including new files, using a separate temporary Git index
without staging the real worktree. It verifies that the original seven Go files
remain byte-identical to the earlier candidate and that the three
prerequisite-only files have no diff. The two added manifest entries are the Go
FFI guard and its regression test. The completion tracker holds the final patch
identity. Do not stage local verification artifacts.

| Artifact | SHA-256 |
| --- | --- |
| `build/modules/l3b/api/libl3b_cp.a` | `3e8c6828346c8a56f1df82d5b5866f869713d97dd24add95fbfdd6856ff43990` |
| `build/modules/l3b/dataplane/libl3b_dp.a` | `a1f69802359a38ee15636510cb9fbd80fa2e4ee879f98716007821dc240a91eb` |
| `build/objects/l3b/api/libl3b_objects.a` | `af2ced3795ac9eead384aaeafa2a64c771381886a5538485f56985d87cbb5834` |
| `build/lib/dataplane/config/libconfig_dp.a` | `429d3913dbeee47456f97e99ceb9c7a4a4e5a0ef5b5f68c4e414d0a247850c6a` |
| `.verify/f1-bounded/shm.test` | `9e86e1a74a57802679343d6a39d9fcfd469a0ce054d5792f498e855db718760d` |
| `.verify/f1-bounded/nontraffic.jsonl` | `92cdd07efdd5ae9ac87f065c4db13477158fa949d1b7933821074f2816d1b81c` |
| `.verify/f1-bounded/race.jsonl` | `c81fba72f5f50a4d70c10183f38639ade245c095a766ffa304f81f223ef1657a` |

The container does not mount the primary Git metadata. Meson's build revision
stamp is not revision provenance; the host-confirmed baseline, complete patch
and freshly generated artifact identities provide that provenance instead.

The earlier full patch
`6ac82d69aa122008d9a9fa3e444e109a1432794b9061d4511f00f774f4678640`
was approved by separate full review task `ses_f2d246d33ffeWV6T0A1cKQFcTS`.
That approval is historical context, not approval of this final-baseline patch.
Only its admission delta was transferred; final documentation records the
merged prerequisite and the newly executed gates. Old evidence remains intact.

Independent fresh-context full review has not run for this candidate. The lead
must provide Blind Hunter, Edge Case Hunter, Verification Gap Reviewer and
Acceptance Auditor results, resolve confirmed blockers and rerun affected gates
before publication. This record establishes bounded implementation evidence,
not full release qualification or permission to enable traffic.
