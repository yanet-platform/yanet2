# L3B Nontraffic Admission

This document describes the nontraffic coverage for [#2721](https://github.com/yanet-platform/yanet2/issues/2721). It covers loading the existing L3B module and object types, constructing the control plane, service discovery, empty-state reads, and bounded startup and shutdown. It does not qualify packet processing, production trust, or release readiness.

## Layout

Unlike modules whose dataplane types are co-located, L3B keeps packet processing in `modules/l3b` and shared virtual-service and session-table types in `objects/l3b`; module configuration links to those types by name. The scope owner, @moonug, approved retaining this split for #2721 on 2026-09-24. L3B uses the common `ffi.AttachConfig` and `ffi.Attachment` lifecycle introduced by [#2784](https://github.com/yanet-platform/yanet2/pull/2784).

## Verified Coverage

| Behavior | Evidence |
| --- | --- |
| Production loaders discover one L3B module and both object types; unknown names fail without changing inventories | [`common/go/testutils/shm/shm_test.go`](../common/go/testutils/shm/shm_test.go) |
| Direct construction uses file-backed memory and the 64 MiB L3B default | [`modules/l3b/controlplane/mod_test.go`](../modules/l3b/controlplane/mod_test.go), [`modules/l3b/controlplane/cfg_test.go`](../modules/l3b/controlplane/cfg_test.go) |
| Director independently constructs the Bundle and Gateway with one discoverable L3B service | [`controlplane/yncp/director_test.go`](../controlplane/yncp/director_test.go) |
| Empty service and config reads return empty results; reads for absent services or sessions return `NotFound` | [`modules/l3b/controlplane/mod_test.go`](../modules/l3b/controlplane/mod_test.go), [`controlplane/yncp/director_test.go`](../controlplane/yncp/director_test.go) |
| Cancellation, canceled startup, listener failure, and partial-construction failures release owned resources | [`modules/l3b/controlplane/mod_test.go`](../modules/l3b/controlplane/mod_test.go), [`controlplane/yncp/director_test.go`](../controlplane/yncp/director_test.go), [`controlplane/bundle/cfg_test.go`](../controlplane/bundle/cfg_test.go) |
| Shared-memory attachment rejects out-of-range instance IDs before unsafe C traversal | [`controlplane/ffi/shm_test.go`](../controlplane/ffi/shm_test.go) |

The fixture uses production module and object loaders and real constructor symbols. It loads object type descriptors only; it does not create virtual-service or session-table instances. No workers, packet loop, active module configuration, enabled reals, or scheduler are started.

For this inventory, supported means verified within this nontraffic scope. The file-backed fixture is transitional test infrastructure, not a deployment mode. Removing its mapping releases the complete test segment; it does not prove that agent detach reclaims arena allocations. Workers, packet processing, active configuration, enabled reals, and scheduler activation are excluded.

## Applicability And Remaining Work

These tests establish bounded nontraffic construction and lifecycle behavior, not production or traffic support.

| Area | Status | Owner |
| --- | --- | --- |
| Ready-instance startup and Run cancellation | Tested for the empty runtime | @moonug for qualification beyond this scope |
| Fixed readiness timeout for not-ready memory | Not tested; uninitialized-memory tests assert immediate rejection, not the Director's timeout behavior | @moonug |
| Cancellation during long-running L3B backend work | Not proven; empty reads do not exercise long-running operations | @moonug |
| Production trust and authorization | Not proven by local plaintext construction or generic Gateway tests | @moonug |
| Reclamation of arena allocations on agent detach | Not proven; tests release the complete file-backed mapping | @moonug |
| Release lifetime, packet-path, and traffic qualification | Not tested; requires a separate approved scope | @moonug |

## Execution Evidence

Runtime test results for the unchanged code are summarized in the [execution evidence comment](https://github.com/yanet-platform/yanet2/pull/2801#issuecomment-5823427278). The [PR checks page](https://github.com/yanet-platform/yanet2/pull/2801/checks) contains CI results and logs; current revision and review status are shown on [PR #2801](https://github.com/yanet-platform/yanet2/pull/2801). These records do not imply release or traffic approval.
