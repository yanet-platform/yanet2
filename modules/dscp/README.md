# DSCP marking

The module selects IPv4 and IPv6 packets by source and destination prefixes,
then applies its marking mode. It preserves ECN and updates the IPv4 header
checksum when marking changes the DSCP value.

The generic operator loads an `UpdateConfig` request such as:

```yaml
name: dscp0
config:
  source_prefixes4: [192.0.2.0/24]
  source_prefixes6: ["2001:db8:1::/48"]
  destination_prefixes4: [198.51.100.0/24]
  destination_prefixes6: ["2001:db8:2::/48"]
  dscp_config:
    flag: 2
    mark: 46
```

The method is
`modules.dscp.controlplane.dscppb.v1.DscpService/UpdateConfig`.
Each update replaces the whole named configuration, including both prefix
sets and marking settings.

`source_prefixes4` and `source_prefixes6` select sources.
`destination_prefixes4` and `destination_prefixes6` select destinations.
Each direction matches any of its prefixes for the packet's address family,
and the two directions combine with **AND**:

| Populated directions | Selection |
| --- | --- |
| Source only | The source must match; the destination is unrestricted. |
| Destination only | The destination must match; the source is unrestricted. |
| Both | Both source and destination must match. |
| Neither | No packets are marked. |

An omitted list and an empty list have the same meaning. A populated direction
with no prefixes for the packet's address family never matches: IPv4 source
prefixes combined with only IPv6 destination prefixes select no packets.

Marking modes are `0` (`never`), `1` (`default`, only when the incoming DSCP is
zero), and `2` (`always`). Marks range from 0 to 63. An omitted marking setting
defaults to `never`. Unselected packets pass unchanged.

The CLI accepts `--source-prefix` and `--destination-prefix`:

```bash
# Source-only selection.
yanet-cli-dscp update --name dscp0 --source-prefix 192.0.2.0/24 --flag always --mark 46

# Both directions must match.
yanet-cli-dscp update --name dscp0 --source-prefix 192.0.2.0/24 --destination-prefix 198.51.100.0/24 --flag always --mark 46
```

Repeat either flag to supply more prefixes, including IPv6. `show --name dscp0`
displays sources before destinations. The CLI retains `--prefix` and `-p` as
aliases for `--destination-prefix`.

The generic operator accepts the legacy YAML keys `prefixes4` and `prefixes6`
as aliases for `destination_prefixes4` and `destination_prefixes6`. Specify only
one spelling per address family; providing both is an error. Existing YAML
keeps its destination-only behavior without adding source fields. New API
clients use the explicit direction names; protobuf field numbers are unchanged.

When a configuration must also be read by older code, use the legacy YAML
destination aliases. Older permissive readers ignore source fields and keep
destination-only behavior; a source-only config selects no packets there.
