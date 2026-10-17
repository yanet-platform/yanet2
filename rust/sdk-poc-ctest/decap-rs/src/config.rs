//! Mirror of the decap module's configuration body.

use yanet_sdk::Lpm;
use zerocopy::{FromBytes, KnownLayout};

/// Fields of `struct decap_module_config` after its `cp_module` header.
///
/// `modules/decap/dataplane/config.h` is the source of truth: the C control
/// plane builds this configuration, and `decap-rs/systest` checks this
/// mirror against that header, field by field, with the meson flags.
#[derive(FromBytes, KnownLayout)]
#[repr(C)]
pub struct DecapConfig {
    /// Tunnel endpoints matched on the outer IPv4 destination.
    pub prefixes4: Lpm,
    /// Tunnel endpoints matched on the outer IPv6 destination.
    pub prefixes6: Lpm,
}
