# Upgrade notes

Manual steps required when upgrading an existing deployment. The packaging
does not perform these automatically.

## FWState synchronization moves to an endpoint list

`SyncConfig` replaces its flat source, destination MAC and multicast/unicast
endpoint fields with a repeated `endpoints` list: each entry carries its own
source and destination IPv6 address and a destination port. An update that
carries endpoints replaces the whole list; `no_endpoints` clears it. The
shared-memory config layout changes, so the dataplane and the control plane
must be upgraded together.

The `dst_ether` field is gone: emitted sync packets leave their Ethernet
header zeroed, and the route module downstream of fwstate resolves and stamps
both MAC addresses. A fwstate config must sit in front of route in the
pipeline, and a multicast sync group needs a covering route whose next-hop
MAC is the derived multicast MAC (for example `33:33:00:00:00:01` for
`ff02::1`). The UDP source port of every emitted packet is randomized into
the ephemeral range, so sync traffic spreads over equal-cost paths.

The CLI replaces `--src-addr`, `--dst-ether`, `--multicast` and `--unicast`
with a repeatable `--endpoint SRC@DST:PORT` (for example
`fd00::1@ff02::1:9999`, the destination also accepts brackets) and
`--no-endpoints`. Upgrade `yanet2-cli`, the control plane and the web UI
together: an old CLI's endpoint flags fail as unknown, and an old service
rejects the new request's endpoint list as incomplete.

## Gateway HTTP rejects cross-origin browser calls from untrusted origins

The gateway's HTTP server no longer sends `Access-Control-Allow-Origin: *`.
A cross-origin browser POST is now rejected with 403 unless the page's
origin is listed in `server.http.trusted_origins`, and only a listed origin
gets CORS headers. Same-origin pages, the web dev proxy and clients that are
not browsers, such as curl and scripts, are unaffected.

A web UI served from another origin than the gateway, a standalone UI or a
gateway added to the UI's Gateway drawer, stops working until every such
gateway lists the UI's origin:

```yaml
gateway:
  server:
    http:
      endpoint: "[::1]:8081"
      trusted_origins:
        - "https://yanet-ui.example.net"
```

Each entry is a bare `scheme://host[:port]`; anything else fails the config
load. The deprecated `server.http_endpoint` has no place for origins, so a
gateway still on it moves to the `server.http` section to trust one.

## TLS key pairs move into `key_pairs` and rotate without a restart

Every certificate and private key is now given as a `key_pairs` list
instead of `cert_file` and `key_file`: in the gateway's `server.grpc.tls`,
`server.http.tls` and deprecated `server.tls`, and in every client `tls`
block (`gateways[].tls` of the operators, `route_operator_tls` of the BIRD
adapter). Each entry names a loader type and its settings, and `file` is
the only type. `ca_file` and `server_name` stay as they are. Rewrite

```yaml
tls:
  cert_file: /etc/yanet2/tls/server.pem
  key_file: /etc/yanet2/tls/server.key
```

into

```yaml
tls:
  key_pairs:
    - type: file
      config:
        cert_file: /etc/yanet2/tls/server.pem
        key_file: /etc/yanet2/tls/server.key
```

A gateway TLS listener needs at least one entry, so a gateway config left
with only the old keys fails to load and names the listener. A client may
give no entry and then presents no certificate, and an operator `tls`
block left with only `cert_file` and `key_file` dials without one, since
the old keys are ignored like any unknown key. Keep `cert_file` and
`key_file` next to `key_pairs` through the package upgrade, as the older
binaries ignore `key_pairs` and the newer ones the old keys, and drop the
old keys afterwards. The BIRD adapter rejects unknown keys in both
versions, so its `route_operator_tls` moves to `key_pairs` together with
the package. An unknown `type`, or an unknown key under `config`, fails
the config load. Environment overrides cannot reach the settings under
`config`, so key pair paths are YAML-only.

The files are reread without a restart. The gateway and every client
reload an entry on the first handshake an hour after its last try, the
gateway also on a metrics scrape, and on every handshake while its
certificate expires within the hour or already has. A loaded pair replaces
the current one only when it parses, its key matches and it is valid now.
A pair not yet valid waits and takes over at its start without another
read; anything else keeps the current pair, with a warning naming the
source once per content. Replace the files by rename or by swapping a
symlink: a key and certificate written one after the other may be read
half written, which keeps the old pair until both files match at a later
reload. Startup still fails on an unreadable or mismatched pair, and a
pair outside its validity period still loads, with a warning.

The gateway serves the first entry that is valid and that the client
supports, else the first entry. A client presents the first entry that is
valid and that the server's request accepts, else no certificate, so an
expired client certificate leaves its calls anonymous instead of failing
the handshake.

A gRPC connection keeps the client certificate of its handshake, while the
x509 authenticator checks that certificate on every call. Set
`server.grpc.tls.max_connection_age` to make every client reconnect:

```yaml
gateway:
  server:
    grpc:
      tls:
        endpoint: "[::1]:8443"
        max_connection_age: 1h
        key_pairs:
          - type: file
            config:
              cert_file: /etc/yanet2/tls/server.pem
              key_file: /etc/yanet2/tls/server.key
```

Once a connection reaches that age, varied by up to 10% either way, the
gateway sends GOAWAY: calls and streams in flight run to completion and the
client reconnects on its next call. That listener also refuses TLS session
resumption, which would carry an old client certificate over. The key is
off by default, and a gateway with an x509 authenticator warns at startup
while it is unset. Under `server.http.tls`, negative, or positive but under
a second, it fails startup.

Deploy a renewed client pair more than the reload interval plus 1.1 times
the connection age before the old one expires, 1h + 1.1 × 1h = 2h06m with
the example above: the first handshake an hour after the last reload picks
the pair up, and within 1.1 times the age every older connection has had
its GOAWAY, so new calls go out presenting the new pair. A stream already
running at the GOAWAY keeps its connection until it ends, since its
identity was checked when it started. Renewing a day ahead satisfies this
with room to spare. A server pair only needs the hour, since clients check
the server certificate at the handshake alone.

To move client certificates onto a new CA:

1. Add the new CA to the x509 authenticator's `ca_sources` on every
   gateway; the gateway reloads them by itself.
2. Replace the pair files of every client with a certificate from the new
   CA, as for a renewal, without a restart. A second `key_pairs` entry, a
   config change and so a restart, is needed only while a client talks to
   gateways that do not all trust the new CA yet. A client then presents
   the first entry whose issuer the gateway's request names, so the two
   CAs need distinct subject names.
3. Drop the old CA from `ca_sources` last.

To move the gateway's server certificate onto a new CA, add the new CA to
the `ca_file` of every client first, which a client reads only when it
starts, then replace the gateway's pair files.

Two gauges follow a rollout, see [gateway TLS metrics](metrics.md#gateway-tls-metrics):
`gateway_tls_certificate_not_after_seconds` for the certificates the
gateway serves and `auth_x509_client_certificate_not_after_seconds` for
those its clients present.

## Gateway listeners move into `grpc` and `http` sections

The gateway's `server` block now configures each protocol in a section of
its own, `grpc` and `http`. A section takes an optional plaintext `endpoint`
and an optional `tls` listener with its own `endpoint` and `key_pairs`, so
plaintext and TLS can run side by side while clients move to TLS one at a
time.

The old `endpoint`, `http_endpoint` and `tls` keys keep working as
deprecated aliases until #2983 removes them, and the director logs a
warning naming every one it finds. `endpoint` maps to `grpc.endpoint` and
`http_endpoint` to `http.endpoint`; with `tls` set, both turn into the TLS
listener of their section instead, as before. `[::1]:8080` stays the gRPC
default only while neither `endpoint` nor `grpc` is given. A section
replaces the old keys of its protocol, so old keys left beside it are
ignored and named in the warning.

The packaged `default.yaml` keeps the old keys until #2983, so it logs the
warning, and the new sections appear in it as a commented example. A site
config kept by dpkg keeps its old keys and keeps working; to move it,
rewrite for example

```yaml
gateway:
  server:
    endpoint: "[::1]:8080"
    http_endpoint: "[::1]:8081"
    tls:
      key_pairs:
        - type: file
          config:
            cert_file: /etc/yanet2/tls/server.pem
            key_file: /etc/yanet2/tls/server.key
```

into the same listeners:

```yaml
gateway:
  server:
    grpc:
      tls:
        endpoint: "[::1]:8080"
        key_pairs:
          - type: file
            config:
              cert_file: /etc/yanet2/tls/server.pem
              key_file: /etc/yanet2/tls/server.key
    http:
      tls:
        endpoint: "[::1]:8081"
        key_pairs:
          - type: file
            config:
              cert_file: /etc/yanet2/tls/server.pem
              key_file: /etc/yanet2/tls/server.key
```

Adding `grpc.endpoint` on another address then serves plaintext gRPC beside
TLS for the clients that have not moved yet. An x509 authenticator needs
the gRPC TLS listener: only that listener asks for client certificates,
plaintext callers stay anonymous, and HTTPS never asks for one.

Environment overrides follow the keys: `YANET_GATEWAY_SERVER_GRPC_ENDPOINT`,
`YANET_GATEWAY_SERVER_GRPC_TLS_ENDPOINT`,
`YANET_GATEWAY_SERVER_GRPC_TLS_MAX_CONNECTION_AGE` and so on for the
sections, while key pair paths are YAML-only. Once a config uses a section,
overrides of that protocol's old keys are ignored like the keys themselves.
Until #2983, `YANET_GATEWAY_SERVER_HTTP_ENDPOINT` names both
`http_endpoint` and `http.endpoint`, and setting it fails startup.

## Route operator separates device exclusions from unresolved nexthops

`route_operator_fib_unresolved_nexthops` now counts route records whose
nexthops are absent from the full neighbour snapshot. Healthy routes on
foreign devices no longer inflate this metric. The new
`route_operator_fib_device_excluded_routes` gauge counts resolved route
records excluded by the gateway's device restriction, with `gateway` and
`module` labels.

Both metrics are latest-build gauges: subsequent builds replace their
values, including resetting them to zero. They are not counters or
confirmations of successful gateway programming. Missing neighbours have
unknown device ownership, so do not sum per-gateway unresolved series as
unique global losses. No configuration migration is needed.

## Pdump adds an optional capture rate limit

Pdump now supports an optional `rate_pps` setting with one best-effort
rate/burst budget per config shared across all workers. The accepted range
is 0..1,000,000 pps, inclusive. New configs default to zero (unlimited),
so existing configurations do not need a rate setting. Omitting the rate
on an update preserves its current value. Burst is automatic and global,
not per worker. See
[shared capture limit](ring.md#shared-capture-limit) for replacement,
recommit and `pdump_rate_skipped` counter semantics.

The private shared-memory pdump config layout changed. Rebuild the
dataplane, pdump plugin and CGO control-plane binaries from the same
revision, then stop the old dataplane and all processes attached to its
shared memory in a coordinated maintenance window. Remove stale
`/dev/hugepages/yanet*` mappings only after those processes have stopped,
then restart the rebuilt components and reapply configuration. Do not mix
old/new binaries or reuse pre-upgrade shared memory. For source builds,
run `meson compile -C build` to rebuild C archives and CGO dependency
fingerprints, then rebuild standalone CGO executables. The plugin interface
ABI does not version this private layout; there is no mixed-version
negotiation or rolling compatibility here.

## Pdump capture moved onto named ring objects

Pdump no longer sizes a private per-worker ring. A config captures into a
pre-existing ring object instead, created once and named.

The record layout and the shared-memory config it lives in changed
together, so upgrade the dataplane and the control plane in the same
maintenance window — a dataplane still running the old private-ring code
cannot read a config the new control plane writes, and vice versa. Clear
any stale `/dev/hugepages/yanet*` files from before the upgrade.

1. Create the ring before creating or updating any pdump config that
   captures into it:

   ```bash
   yanet-cli-ring create --name captures --capacity 1MiB
   ```

   The pdump web page can create a ring from its own UI as well. Pdump
   binds only to a ring of at least 128 KiB per worker, so every record
   fits whatever the snaplen; a smaller ring is refused
   (`InvalidArgument`).

2. Pass the ring's name instead of a size. `ring_size` and `--ring-size`
   are gone, along with the web ring-size input; `ring_name` /
   `--ring-name` (the web config dialog's ring field) replaces them. It
   is required when a config is created. An update that omits it keeps
   the config's currently bound ring; passing it explicitly empty is
   rejected.

   Rings allocate in the pdump module's own agent arena, not a separate
   reserve. A deployment that overrides pdump's `memory_requirements`
   (16 MiB by default before this change) must raise it to cover its
   rings; the packaged default is now 128 MiB.

Old automation that still passes `--ring-size`, or that creates a config
without naming a ring, now fails: an absent or empty ring name is
rejected (`InvalidArgument`), and a name that does not match a published
ring is rejected (`NotFound`). Nothing is created on either failure.

A ring linked by a published pdump config cannot be deleted — delete the
config first. As before, deleting a config ends its capture streams; open
readers do not keep the ring from being deleted. Deleting a config never
deletes the ring it captured into.

A capture stream no longer replays the records a ring already held
before the stream started — including a stream reopened after rebinding
a config to another ring. It starts at the ring's current position. A
same-object config update (filter, snaplen or mode, ring kept) still
carries its stream and cursor forward without a reset.

PCAP and PCAPNG files pdump's CLI writes always carry a 65535 header
snaplen now, independent of the configured capture snaplen.

Field 4 is removed and reserved, not renumbered: a new control plane
simply ignores an old CLI's `ring_size` as an unknown field, and since an
old CLI cannot send `ring_name`, its create fails with
`InvalidArgument`; an old CLI's `show` just lacks the ring. Upgrade
`yanet2-cli` together with the control plane.

## Balancer2 scaffold and CLI removed

The unused `modules/balancer2` scaffold, the
`modules.balancer2.controlplane.balancerpb.v1.Balancer` protobuf service,
`yanet-cli-balancer2`, and the unsupported root `balancer.yaml` example
have been removed. The generic operator no longer links those request
descriptors. Configurations targeting the removed service are rejected.

Remove references to the old CLI and service from automation before
upgrading. The existing `l3b` module and `yanet-cli-l3b` remain unchanged,
but are not wire- or YAML-compatible aliases for Balancer2. Translate only
operations supported by their actual API; renaming the binary or module
does not convert a configuration. This removal does not establish complete
feature parity between the two surfaces.

## FWState updates carry field presence

Upgrade the fwstate control plane and the web UI before using the new CLI.
`UpdateConfigRequest` no longer has `update_mask`, `clear_multicast` or
`clear_unicast`. The map names, sync ports and timeouts are `optional` fields
instead. A request changes only the fields it carries, zero and empty values
included, and an absent field keeps the stored value. The service merges the
carried fields under its mutation lock, so updates to different fields
preserve each other. A request naming only the config creates a missing one
with defaults.

An explicit zero port disables its endpoint and clears the endpoint address.
The CLI encodes `--no-multicast` and `--no-unicast` this way. A request that
carries an endpoint address together with a zero port for the same endpoint
is rejected. `ShowConfig` JSON now includes zero ports and timeouts.

Mixed versions misbehave. An older CLI talking to the new service loses
explicit zeros, so `--no-multicast`, `--no-unicast` and zero timeouts do
nothing. The new CLI talking to an older service gets the legacy merge, where
supplying one endpoint disables the other and a zero port keeps the stored
one.

## Controlplane systemd unit became a template

`yanet2-controlplane.service` is replaced by the template
`yanet2-controlplane@.service`. The instance name selects the config:
`yanet2-controlplane@<name>` runs `/etc/yanet2/controlplane.d/<name>.yaml`.

Upgrading does not stop or disable the old unit, so its process keeps holding
`[::1]:8080` and `/dev/hugepages/yanet`. Enabling an instance before removing
it gives you two control planes fighting over both. On each host:

1. Retire the old unit:

   ```bash
   systemctl disable --now yanet2-controlplane.service
   systemctl daemon-reload
   ```

2. Move the site config into place. The old unit read
   `/etc/yanet2/controlplane.yaml`, which nothing reads any more:

   ```bash
   mv /etc/yanet2/controlplane.yaml /etc/yanet2/controlplane.d/default.yaml
   ```

   The packaged example moves itself from
   `/etc/yanet2/controlplane-default.yaml` to
   `/etc/yanet2/controlplane.d/default.yaml` during the upgrade, so it is
   already there if you never wrote a site config. It logs at `debug` and
   binds `[::1]:8080` — review it before running it in production.

3. Enable the instance. No instance is enabled by the package:

   ```bash
   systemctl enable --now yanet2-controlplane@default
   ```

Instances are grouped under `yanet2-controlplane.target`, which the operators
order against, so `systemctl restart yanet2-controlplane.target` reaches every
running instance.

Running more than one instance needs per-instance listener endpoints
(`grpc` and `http`, or the deprecated `endpoint` and `http_endpoint`) and
`memory_path` values in each config, and the operators point at
`[::1]:8080` in their own configs.

## `instance_id` is now mandatory on the gateway and on every module and device

The gateway and every module or device block in the controlplane config now require an explicit `instance_id`, naming which dataplane instance that entry serves. `0` remains a perfectly valid value, it just has to be written down. A config that omits `instance_id` anywhere it applies fails to load, and the director refuses to start.

This is a dpkg conffile, so an upgrade preserves your site-modified copy of `/etc/yanet2/controlplane.d/<name>.yaml` unchanged. Since every config written before this change predates `instance_id`, the director will refuse to start right after the package upgrade until you edit it in.

The same change also makes the module and device list exhaustive: a module or device left out of the config is no longer started at all, and no longer attaches a shared-memory agent. Before this change every bundled module started regardless of whether the config mentioned it, defaulting to instance 0 — which is the production defect this fixes. Add an explicit block for every module and device you actually rely on; do not assume the old implicit set still starts.

1. Add `instance_id` to the `gateway:` block and to every `modules:`/`devices:` entry in your site config:

   ```yaml
   gateway:
     instance_id: 0
     server:
       endpoint: "[::1]:8080"
     ...
   modules:
     route:
       instance_id: 0
       ...
   devices:
     plain:
       instance_id: 0
       ...
   ```

2. Add a block for every module and device your deployment actually uses, even if it previously relied on the implicit default set. Use the packaged `/etc/yanet2/controlplane.d/default.yaml` as the worked example of the new shape — it lists every bundled module and the `plain`/`vlan` devices with `instance_id: 0`, but omits `trafgen`. If your deployment uses the trafgen device, add its `instance_id`-carrying block yourself; the packaged example does not do it for you.

## Firewall state moved to named fwstate-map objects

Stateful ACL setups no longer link an ACL config to a fwstate config through
the gateway. State tables are standalone `fwstate-map` objects created by
name, and both the ACL and fwstate module configs reference them by name.

1. Add the `fwstate` module to the controlplane config. It attaches its own
   agent and hosts the fwstate and fwstate-map services; the ACL agent no
   longer serves them. A stateful setup whose config does not list the
   `fwstate` module silently loses those services:

   ```yaml
   modules:
     fwstate:
       instance_id: 0
       memory_path: *memory_path
       memory_requirements: 8GB
   ```

   The fwstate-map objects linked by acl and fwstate configs allocate in
   this module's agent zone — size `memory_requirements` for the state
   tables.

2. Provision the maps with the new CLI, one per address family, and record
   the names:

   ```bash
   yanet-cli-fwstatemap create --name fw4 --kind v4
   yanet-cli-fwstatemap create --name fw6 --kind v6
   ```

   Map introspection (list, stats, entries, layer rotation) lives in
   `yanet-cli-fwstatemap`; the fwstate CLI keeps only its config commands,
   and the fwstate service no longer serves `GetStats`/`ListEntries`.

3. Reference the names from both modules — `map_name_v4`/`map_name_v6` on
   the fwstate config (both required), `fwtable_name_v4`/`fwtable_name_v6`
   on the ACL config (optional; an empty name means that family's
   CHECK_STATE finds no state). The old link workflow (the `LinkFWState`
   RPC, the CLI link subcommand, the web link tab) is gone.

An update naming a map that is not published is rejected, and a map cannot
be deleted while any published module config links it — update or delete
the linking module first. The proto field renumbering on the two rewritten
services means an old CLI against a new control plane misdecodes requests:
upgrade the `yanet2-cli` package together with the control plane.

## CLI flags follow one dictionary

The file a command consumes is its positional argument now. Five flags are
gone without an alias:

```bash
yanet-cli-acl update --name acl0 acl0.yaml                    # was --rules
yanet-cli-mirror update --name mirror0 mirror0.yaml           # was --rules
yanet-cli-route fib update --name route0 route0.yaml          # was --rules
yanet-cli-unrdup update --name unrdup0 unrdup0.yaml           # was --config
yanet-cli-device-trafgen upload --name gen0 replay.pcap       # was --pcap
```

`yanet-cli-counters` takes its name patterns as positional arguments
(`yanet-cli-counters 'rx_.*' --device eth0`), `--name` is gone.
`yanet-cli-dscp set-marking --flag` takes `never`, `default` or `always`
instead of `0`, `1`, `2`.

Every short letter means one thing in every binary: `-n` name, `-d`
device, `-p` pipeline or prefix, `-f` function, `-c` chain, `-t` tag.
Check scripts that pass `yanet-cli-acl metrics-rules -c`: it selects a chain
now, and the config is `--name`. The other displaced letters (`-t` in
counters, `-f` and `-s` in pdump) and the spellings `--config` on `acl metrics-rules` and
`--prefixes` on `decap update` keep working as hidden aliases for one
release.

## Mirror rule files spell the wire request

`yanet-cli-mirror update` reads the shape the generic operator pushes and
`show` prints, exactly like forward: a rule carries an `action` object
(`target`, `mode`, `counter`) and its `devices` as named objects. The
previous flat rule form is refused, and `--format json` prints the wire
message.
