# yanet-packet

Byte-level packet header library for the YANET2 Rust module SDK. It ports the
header handling of the C packet library and grows on demand.

## Scope

- Ethernet, 802.1Q VLAN tag, IPv4 (options aware), IPv6 fixed header, TCP,
  UDP, ICMPv4, ICMPv6.
- RFC 1624 incremental checksum update with the exact semantics of the C
  checksum helpers, plus full one's-complement and L4 checksums over the IPv4
  and IPv6 pseudo headers.
- Typed in-place views over `&[u8]` / `&mut [u8]`. A view is built at the
  offset where a header starts and refuses short or malformed input by
  returning a `ParseError` that says why. Two checks differ from the C
  parser: an IPv4 view also refuses a version other than 4, and `has_vlan`
  also reports the 802.1ad tag 0x88a8, which the C parser treats as an
  unknown L3 type.
- Setters keep checksums consistent incrementally where the C code does (TTL,
  DSCP, addresses, ports). Rewriting an address also requires a call to the
  L4 header to fix the pseudo-header checksum.

Out of scope for now: DSCP policy, MSS clamping, encapsulation and
decapsulation.

## no_std and safety

The crate is `#![no_std]`, uses no allocator, and is
`#![forbid(unsafe_code)]`. Parsing is done by `zerocopy` derives; no function
panics on attacker-controlled bytes. Buffers only: no mbuf, no DPDK types.

The headers implement the `zerocopy` traits, so `zerocopy` is a public
dependency: a new major version of it is a breaking change here. Those traits
also map bytes onto a header without the checks of `parse`; only a view from
`parse` is a validated one.

MAC addresses are `netip::MacAddr` from the YANET `netip` crate, also
`no_std` without dependencies; it is a public dependency too.

The crate has no path dependencies and inherits nothing from a workspace, so
it can be moved to its own repository as is.

## Testing

The checks CI runs on the crate:

```
cargo test                                   # standalone, outside the workspace
cargo check --target x86_64-unknown-none     # standalone: no std, no alloc
```

Inside the SDK workspace it is also linted by the workspace's
`cargo clippy --workspace --all-targets -- -D warnings`.
