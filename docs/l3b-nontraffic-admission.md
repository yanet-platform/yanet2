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
