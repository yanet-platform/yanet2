# fwstate

## Synchronization pipeline placement

An ACL module whose rules can create state must be immediately followed by the
fwstate module that consumes its synchronization events:

```text
acl:<producer> -> fwstate:<consumer> -> ...
```

Do not place another packet-processing module between this ACL and fwstate.
Internally generated synchronization events carry a trusted metadata flag, but
their destination address and UDP port remain unset until fwstate applies its
configured synchronization destination. The flag identifies the event to the
adjacent fwstate consumer; it does not make the event bypass intervening
modules.

Pipelines that violate this ordering are unsupported and may silently lose
synchronization events. Pipeline configuration is user-controlled, so users
are responsible for preserving this adjacency.
