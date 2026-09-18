# Deployment endpoint overrides

The shared command lifecycle supports `YANET_KUBERNETES_GATEWAYS` for route,
pipeline, generic and neighbour-sidecar operators. It is a JSON array containing
the complete active gateway selection, in its desired order:

```json
[{"name":"numa1","endpoint":"yanet-example-controlplane-numa1.example.svc.cluster.local:8080"}]
```

Each name must identify exactly one entry in the loaded host configuration.
The override replaces that entry's endpoint and preserves its TLS settings.
Gateways omitted from the array are removed from the runtime's active list.
Names also retain their meaning in application configuration such as route
`gateway_devices`; use stable physical identities such as `numa0` and `numa1`.
The endpoint must contain a nonempty host and a TCP port in the range 1–65535.

The overlay runs after normal configuration loading and validation, before the
runtime factory opens transports. Malformed JSON, an empty active list, missing
or duplicate names, unknown gateways and invalid endpoints prevent startup.
An unset variable leaves the configuration unchanged. A config type opts into
this contract by exposing `GatewayConfigs() *[]GatewayConfig`; other command
consumers ignore the variable.

Bind and registration use the existing `YANET_SERVER_ENDPOINT` and
`YANET_SERVER_ADVERTISE_ENDPOINT` variables. A Service-backed deployment binds
its assigned Pod port and advertises the Service's reachable address and port.
Indexed `YANET_GATEWAYS_<index>_ENDPOINT` variables retain the configuration's
unmentioned list entries; they do not express a reduced active gateway set.
