# Gateway readiness

The gateway exposes a single readiness scope describing whether its built-in
service runners have finished initial registration.

- gRPC FQN: `controlplane.ynpb.v1.ReadinessService`
- Service implementation: `readiness.go` (`ReadinessService`).
- Scope logic: `gateway.go`.

## Scopes

| Scope     | Meaning                                                             |
| --------- | ------------------------------------------------------------------ |
| `gateway` | All built-in service runners completed their initial registration. |

The scope starts at `STATE_UNKNOWN` and is latched to `STATE_READY` once every
out-of-process service runner signals `Ready()` (or immediately when there are
no runners). On shutdown `Drain` flips it to `STATE_NOT_READY` with reason
`SHUTTING_DOWN`, and `WithDrainLatch` makes any later `Set` a no-op.

## `observed_at` freshness

`observed_at` is stamped exactly once, at the READY transition, and is never
advanced afterward. There is no periodic refresh, and no configuration
parameter governs it.

A staleness check against `observed_at` is therefore **not applicable** to this
scope: a healthy, long-running gateway reports an ever-growing `observed_at`
age while sitting happily in READY. Base gateway liveness on the `Ready` RPC
returning `STATE_READY`, not on `observed_at` freshness. If a freshness signal
is required, treat `observed_at` age as a lower bound only (the gateway was up
at least this long), never as a staleness failure.
