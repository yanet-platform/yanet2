# L3B Nontraffic Admission

This document defines the nontraffic coverage for [#2721](https://github.com/yanet-platform/yanet2/issues/2721): loading the existing L3B module and object types, constructing the control plane, service discovery, empty-state reads, and bounded startup and shutdown. It does not qualify packet processing, production trust, or release readiness.

## Layout

L3B keeps packet processing in `modules/l3b` and shared virtual-service and session-table types in `objects/l3b`. The module links to those object types by name. Type constructors and identities remain unchanged. L3B uses the common `ffi.AttachConfig` and `ffi.Attachment` lifecycle introduced by [#2784](https://github.com/yanet-platform/yanet2/pull/2784).

## Maintainer Approval

@moonug, a YANET2 repository maintainer, approved retaining the existing `modules/l3b` and `objects/l3b` split for this nontraffic admission scope on 2026-09-24. This approval is limited to preserving the layout and does not qualify packet processing, production trust, or release readiness.

## Verified Coverage

| Behavior | Evidence |
| --- | --- |
| Production loaders discover one L3B module and both object types; unknown names fail without changing inventories | [`modules/l3b/internal/testutils/shm_test.go`](../modules/l3b/internal/testutils/shm_test.go) |
| Direct construction uses file-backed memory and the 64 MiB L3B default | [`modules/l3b/controlplane/mod_test.go`](../modules/l3b/controlplane/mod_test.go), [`modules/l3b/controlplane/cfg_test.go`](../modules/l3b/controlplane/cfg_test.go) |
| Director constructs the Bundle and Gateway with one discoverable L3B service | [`modules/l3b/tests/nontraffic/admission_test.go`](../modules/l3b/tests/nontraffic/admission_test.go) |
| Empty service and config reads return empty results; service reads for absent names return `NotFound` | [`modules/l3b/controlplane/mod_test.go`](../modules/l3b/controlplane/mod_test.go), [`modules/l3b/tests/nontraffic/admission_test.go`](../modules/l3b/tests/nontraffic/admission_test.go) |
| Cancellation, listener failure, and partial-construction failures release owned resources | [`modules/l3b/controlplane/mod_test.go`](../modules/l3b/controlplane/mod_test.go), [`modules/l3b/tests/nontraffic/admission_test.go`](../modules/l3b/tests/nontraffic/admission_test.go), [`controlplane/bundle/bundle_test.go`](../controlplane/bundle/bundle_test.go), [`controlplane/yncp/director_test.go`](../controlplane/yncp/director_test.go) |
| Shared-memory attachment rejects initialized-but-unpublished storage, admits the last valid instance, rejects unready or out-of-range targets, and rejects embedded NULs in agent attach/extend names | [`controlplane/ffi/shm_test.go`](../controlplane/ffi/shm_test.go) |

The generic fixture in `common/go/testutils/shm` loads no dataplane types by default. L3B tests opt in through an L3B-owned helper that links the production constructors and requests the production loaders to register their type descriptors. The fixture does not create virtual-service or session-table instances. No workers, packet loop, active module configuration, enabled reals, or scheduler are started.

## Verification Record

The approved completion plan baseline is `20e4b1eff7197bd3d7fb3238889ed094b76689a5`. The bounded Docker run used HEAD `8eaa709f18de8b2bb4b8b60847bfdabee59c5a7b`, which already contains prerequisite commit `d0df58e37bb5b68a8c2e7bf96613e10f93efcc58` (#2784). The executable and test delta over that prerequisite has SHA-256 `863b78bad19d39911755347357a23b2604f618df7456cd516cf88cedf2c7608a`; its full patch SHA-256 at verification start was `325d05dc62e25bb6f731b3b723e1ca21fe58de63e0b8847637fa37e9e741945c`. The final approved-baseline-to-candidate identity is recorded in `yanet2-extra/untracked/_bmad-output/implementation-artifacts/new2721-completion-plan-track.md`, under Current Candidate Handoff.

Build and bounded tests ran through `just _docker_run` using the task worktree, its own caches and image `sha256:1f3e0ef3c0271c36ec0432d1ac31fb4a1e930867927e544892b5ec7824d9a7d7` on `linux/amd64` (Ubuntu 24.04.4, Go 1.24.13). The default `just dbuild` and `just dtest` recipes were also attempted but stopped before build/test because their Docker mount omits the linked worktree's common Git metadata. The bounded route adds that metadata as a read-only mount; its exact host-side invocation and environment are recorded in the completion tracker. Preparation generated Go protobufs, reconfigured Meson for `cpu_arch=nehalem`, `dataplane_only=false`, and `debugoptimized`, cleaned the build, and compiled the static archives including libpcap.

The bounded Go gates used these packages and commands inside that container:

```sh
packages=(
  ./common/go/testutils/shm
  ./modules/l3b/controlplane/...
  ./modules/l3b/internal/testutils
  ./modules/l3b/tests/nontraffic
  ./controlplane/bundle
  ./controlplane/yncp
  ./controlplane/gateway
  ./controlplane/ffi
)
make proto-go
meson setup --reconfigure build -Dcpu_arch=nehalem -Ddataplane_only=false -Dbuildtype=debugoptimized
ninja -C build -t clean
meson compile -C build
go test -p 4 -list . "${packages[@]}"
go test -p 4 -json -count=1 -timeout=180s -skip 'Test_(Backend_|L3BService_|RingFromWeights_)|TestWorkerCounters' "${packages[@]}"
go vet "${packages[@]}"
go build ./common/go/testutils/shm ./modules/l3b/controlplane/... ./modules/l3b/internal/testutils ./controlplane/bundle ./controlplane/yncp ./controlplane/gateway ./controlplane/ffi
go test -run='^$' ./modules/l3b/tests/nontraffic
```

Race testing used the same package list, ten repetitions, and this selection:

```sh
go test -p 4 -race -json -count=10 -timeout=180s -run 'Test_(Storage_|NewL3BModule_MissingMemoryFile|L3BModule_Nontraffic|Director_|NewBundle_|Decode_L3BPresence)|Test_SharedMemory_(AgentAttach_(InitializedSegmentBeforePublication|InstanceBounds|UninitialisedSegment|RejectsNULName|RejectsUnreadyTarget)|DataplaneReady_RejectsLaterReadyBeforeFirstPublication|ExtendAgent_RejectsNULName)|Test_Gateway_HostsRunAndCloseEveryService|Test_GatewayRun_ReadinessDrainLatchesLateReady|TestGateway_Run_(ShutsDownWithOpenStream|DrainsReadinessOnShutdown)|Test_InProcessServiceRunner_Run_ShutsDownWithOpenStream|TestGateway_ProxiedRPC_Client(Cancel|Disconnect|Deadline)PropagatesToBackendCtx' "${packages[@]}"
```

- Test discovery confirmed all required admission, cleanup, inventory, and FFI boundary tests. The bounded test run passed 144 top-level tests across nine packages, with zero failures, skips, or excluded-test execution events.
- Race testing passed 33 selected test groups exactly ten times each (330 passes), with zero failures or skips. The fixture-index rejection and successful loader-wrapper tests each passed ten times, alongside the later-instance-before-first-publication, initialized-but-unpublished storage, instance-boundary, partial-readiness, and embedded-NUL cases.
- `go vet`, the package build and compile-only admission test passed. Go and C formatting were clean, and standalone symbol checks found the expected L3B constructors only in the L3B-linked fixture.
- The exact container context, bounded host invocation, standard-recipe failure logs, command trace, test discovery, JSON results, race results, archive hashes, symbols, and candidate patch are retained locally under `.verify/reorg-acquire-20260927-final-rerun/`; these generated artifacts are not part of the pull request.

## Capability Boundary

| Category | Surface |
| --- | --- |
| Supported in this scope | Nontraffic construction, service discovery, empty-state reads, and bounded cleanup on cancellation or constructor failure. |
| Transitional | File-backed shared memory is test infrastructure, not a deployment mode. Removing its mapping releases the complete test segment; it does not prove that agent detach reclaims arena allocations. |
| Excluded | Packet processing, workers, active configuration, enabled reals, scheduler behavior, production trust or authorization, and release qualification. |

## Remaining Qualification

| Area | Status |
| --- | --- |
| Fixed readiness timeout for not-ready memory | Not tested; uninitialized-memory tests assert immediate rejection, not the Director timeout behavior. |
| Cancellation during long-running L3B backend work | Not proven; empty reads do not exercise long-running operations. |
| Production trust and authorization | Not proven by local construction or generic Gateway tests. |
| Reclamation of arena allocations on agent detach | Not proven; tests release the complete file-backed mapping. |
| Release lifetime and traffic qualification | Not tested; requires a separate approved scope. |

The remaining qualification work is owned by @moonug: the fixed readiness timeout, cancellation during long-running backend work, production trust and authorization, arena reclamation, and release-lifetime and traffic qualification. Nontraffic coverage does not imply release or traffic approval.
