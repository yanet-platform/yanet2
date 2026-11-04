# Configuration includes

File-based YAML configurations can compose functional settings and separately
mounted transport settings at load time. The Go configuration loader, native
dataplane, CLI connection configuration and YAML request loaders support the same
top-level `include` list:

```yaml
include:
  - defaults.yaml
  - conf.d
logging:
  level: info
```

Each entry names a local file or directory. Relative entries are resolved against
the file declaring them; absolute paths are accepted. Includes can contain their
own include lists. Paths are literal: shell expansion and glob patterns are not
performed. Only the reserved top-level `include` key is removed before the
component's usual decoding and validation.

## Ordering and merge rules

- Includes are processed in list order; later sources override earlier sources.
- A directory contributes visible `.yaml` and `.yml` files, recursively, with
  entries visited in lexicographic order at each level.
- The declaring file overrides all of its includes, regardless of where the
  `include` key appears in the file.
- Mappings merge recursively. Sequences, scalars, explicit zero/false and null
  replace the earlier value. A sequence is replaced as a whole, including when
  the replacement is empty.
- Component defaults apply before the resulting document. Existing environment
  overrides apply afterward. CLI user-file, alias, environment and flag
  precedence remains unchanged.

For example, a fragment with `server: {endpoint: '[::1]:9000',
advertise_endpoint: 'operator.example:9000'}` and a main file with
`server: {endpoint: '[::1]:9001'}` retain the advertised address and use port 9001
for the local listener.

Validation runs after composition, so required settings can be supplied by
different sources. Invalid YAML, duplicate mapping keys, additional nonempty YAML
documents, missing sources, include cycles and directories containing no YAML
files fail the load. A later overlay does not make an invalid source optional.
Hidden entries are ignored, including Kubernetes `..data` generations. Visible
symlinks are followed, and cycles are checked using resolved paths.

One load permits at most 256 file reads, 16 MiB of cumulative file input and 32
levels below the root source, counting nested directories as levels. Repeated
reads count again. The native loader also bounds YAML expansion to 100,000 copied
nodes and 64 levels of YAML nesting; it does not support YAML merge keys (`<<`).
The L3 load-balancer's included request fragments retain its strict YAML syntax
checks, with nonnegative decimal integer spelling throughout each fragment.

Includes resolve only include paths. Existing component-specific rules for other
paths, such as referenced subnet files or certificate files, are unchanged. Use
absolute certificate paths in transport fragments.

## Operator endpoints and mTLS

For a route operator, keep functional settings in `/etc/yanet2/config/route.yaml`:

```yaml
include: [/etc/yanet2/conf.d]
logging:
  level: info
link_map: {}
```

Mount a transport fragment at
`/etc/yanet2/conf.d/endpoints/transport.yaml`:

```yaml
server:
  endpoint: "[::1]:0"
gateways:
  - name: edge
    endpoint: gateway.example:8443
    tls:
      ca_file: /etc/yanet2/pki/ca.pem
      server_name: gateway.example
      key_pairs:
        - type: file
          config:
            cert_file: /etc/yanet2/pki/client.pem
            key_file: /etc/yanet2/pki/client-key.pem
```

The gateway address and its TLS settings belong in the same list entry. A later
`gateways` list replaces this entire list, including its identities; entries are
not matched or merged by name or index. The same rule applies to `instances`,
`key_pairs` and `authenticators` lists in components that expose those fields.

## Gateway listeners

A control-plane transport fragment can configure native TLS listeners while the
main configuration supplies instance IDs, modules and authorization policy:

```yaml
gateway:
  server:
    grpc:
      tls:
        endpoint: "[::]:8443"
        key_pairs:
          - type: file
            config:
              cert_file: /etc/yanet2/pki/server.pem
              key_file: /etc/yanet2/pki/server-key.pem
    http:
      tls:
        endpoint: "[::]:9443"
        key_pairs:
          - type: file
            config:
              cert_file: /etc/yanet2/pki/server.pem
              key_file: /etc/yanet2/pki/server-key.pem
  auth:
    mode: enabled
    authenticators:
      - type: x509
        config:
          ca_sources: [/etc/yanet2/pki/client-ca.pem]
```

The x509 authenticator supplies client trust; the configured identity and
permission providers determine which authenticated clients can invoke RPCs.
Includes do not alter those authentication or authorization rules.

## CLI connection settings

The system or personal CLI configuration can include the same kind of mounted
directory. For example, `/etc/yanet2/cli.yaml`:

```yaml
include: [cli.d]
endpoint: edge
```

And `/etc/yanet2/cli.d/transport.yaml`:

```yaml
endpoints:
  edge:
    endpoint: grpcs://gateway.example:8443
    ca: /etc/yanet2/pki/ca.pem
    client_cert: /etc/yanet2/pki/client.pem
    client_key: /etc/yanet2/pki/client-key.pem
```

The CLI reports the root system/personal file as the origin of its composed
settings. Include read and syntax errors name the failing source. Module request
files use their existing schemas, with an optional top-level include list;
sequence-root payload files remain ordinary whole-document inputs.

## Kubernetes directory mounts

Give each configuration source its own directory under the included tree:

```text
/etc/yanet2/config/route.yaml
/etc/yanet2/conf.d/
  endpoints/
    transport.yaml
  policy/
    routing.yaml
/etc/yanet2/pki/
  ca.pem
  client.pem
  client-key.pem
```

For example, these Pod spec fields mount whole ConfigMap and Secret directories:

```yaml
containers:
  - name: route
    image: example.invalid/router:latest
    volumeMounts:
      - name: main-config
        mountPath: /etc/yanet2/config
        readOnly: true
      - name: transport-config
        mountPath: /etc/yanet2/conf.d/endpoints
        readOnly: true
      - name: routing-config
        mountPath: /etc/yanet2/conf.d/policy
        readOnly: true
      - name: identity
        mountPath: /etc/yanet2/pki
        readOnly: true
volumes:
  - name: main-config
    configMap:
      name: routing-main
  - name: transport-config
    configMap:
      name: routing-transport
  - name: routing-config
    configMap:
      name: routing-policy
  - name: identity
    secret:
      secretName: routing-identity
```

Start the component with `/etc/yanet2/config/route.yaml` as its configuration.
YAML is assembled in memory when loaded; no merged configuration file is written.
Restart the component to apply changed YAML. Existing PEM refresh behavior is
independent of YAML composition. Certificate private keys belong in the Secret,
while ConfigMaps carry only configuration and file paths.

Directory mounts follow Kubernetes projected-volume symlinks. As documented by
[Kubernetes](https://kubernetes.io/docs/concepts/configuration/configmap/), a
ConfigMap mounted through `subPath` does not receive updates; the directory
layout above uses ordinary directory mounts.

For Go callers, `xcfg.LoadConfig` and `xcfg.ReadFile` resolve includes.
`xcfg.Decode` continues to decode an in-memory document without filesystem I/O.
