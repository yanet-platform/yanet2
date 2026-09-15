# Neighbour discovery sidecar

`yanet-neighbour-sidecar` runs the public `operators/route/neigh` monitor in its
container's network namespace and sends its complete table to the route operator.
Interface provisioning belongs to the separate network-configuration container.

## Configuration

```yaml
gateways:
  - name: route
    endpoint: "[::1]:50051"
table_name: neighbour-sidecar
default_priority: 100
update_interval: 5m
publish_timeout: 5s
link_map:
  eth1: dpdk0
```

Copy the example `yanet-neighbour-sidecar-default.yaml` to
`/etc/yanet2/yanet-neighbour-sidecar.yaml` and adjust it for the deployment. Run
`yanet-neighbour-sidecar -c /etc/yanet2/yanet-neighbour-sidecar.yaml` in the namespace
to observe. The optional `link_map` maps Linux interface names to dataplane device
names; unmapped interfaces retain their Linux names. Each namespace must own a
distinct table. Gateway endpoints may address the route operator directly or proxy
its neighbour service; multiple endpoints are
alternative transports to the **same** route operator, tried in order.

Set `netlink_monitor.disabled: true` in the receiving route operator when neighbour
discovery is delegated to the sidecar. Static neighbour tables remain separate.

## Publication

The monitor's existing full-dump, MAC filtering and event policy determine the
contents: additions trigger a dump, deletion events alone are ignored, and periodic
dumps observe removals. Publication starts after the first successful dump. The
sidecar does not add its own neighbour filtering or discovery implementation.

`SwapNeighbours` replaces the named table through the existing table implementation,
preserving observed state, timestamp, priority and egress information. The sidecar
creates the table if it is absent. Empty snapshots clear it. The common operator
reconciler retries transport failures and periodically republishes the latest
observation, including after a route-operator restart. Its `reconcile` settings use
the common operator defaults. The RPC acknowledges the table update; FIB application
is asynchronous.

## Health and readiness

The sidecar serves HTTP probes on the fixed address `[::]:9903`:

- `GET /healthz` returns HTTP 200 while the server is running, independently of
  gateway availability.
- `GET /readyz` returns HTTP 503 until the route operator acknowledges the first
  complete neighbour-table publication, then HTTP 200. An empty observation also
  counts as a successful publication. Later discovery or publication failures do
  not reset readiness. During shutdown it returns HTTP 503 until the listener
  closes.

Readiness acknowledges initial table delivery; FIB application remains asynchronous.
The HTTP worker shuts down with the process, allowing up to five seconds for active
requests to finish.

Configure Kubernetes probes on the sidecar container:

```yaml
livenessProbe:
  httpGet:
    path: /healthz
    port: 9903
readinessProbe:
  httpGet:
    path: /readyz
    port: 9903
```

## Build and image

The container image builds a static Go binary from source and runs it on Alpine
with CA certificates. Its image-build job runs independently of Debian packaging.
Meson also builds the binary for repository build artifacts.

```sh
docker build -f deploy/yanet-neighbour-sidecar.Dockerfile -t yanet-neighbour-sidecar .
```

Run the image as a sidecar in the Kubernetes Pod whose network namespace it should
observe, and mount its configuration at
`/etc/yanet2/yanet-neighbour-sidecar.yaml`.
