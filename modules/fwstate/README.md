# fwstate

## Synchronization pipeline placement

ACL writes a sync record for every allowed packet that creates or refreshes
state into a per-worker stash of the linked fwstate map. Each round, a
fwstate config on the same map must run after that ACL, anywhere later in the
chain:

```text
acl:<producer> -> ... -> fwstate:<consumer> -> ...
```

Records a round leaves unprocessed are dropped. The first fwstate config to
reach a record inserts or suppresses it; every fwstate config on the map then
sends the applied records to its own destinations once per round, packed up
to `sync_mtu`. A downstream fwstate passes these packets through.

The stash size is set when the map is created: 0 selects room for 64 records,
at most 1 MiB. Records past it are dropped and counted in
`acl_sync_overflow`; the packet verdict does not change.
