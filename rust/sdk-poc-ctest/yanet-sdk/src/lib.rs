//! Module-facing SDK for dataplane modules written in safe Rust.
//!
//! A module crate implements [`Module`] under `#![forbid(unsafe_code)]` and
//! invokes [`export_module!`] once; the exported constructor, the handler
//! trampoline and the descriptor allocation live here. A panic inside a
//! handler aborts the dataplane process: the trampolines are `extern "C"`,
//! which aborts on unwind since Rust 1.81, and release builds use
//! `panic = "abort"`. A module signals a bad packet with [`Verdict::Drop`].

use core::{ffi::c_char, ptr, ptr::NonNull};

pub use yanet_sys::{self as sys, ConfigView, Lpm, LpmView, Packet};
use yanet_sys::{
    Front, Root,
    ffi::{MODULE_TYPE_LEN, YANET_MODULE_ABI_VERSION, dp_worker, module, module_ectx, packet_front},
};
use zerocopy::{FromBytes, KnownLayout};

/// ABI version the exported `yanet_module_abi_version` symbol carries.
pub const ABI_VERSION: u32 = YANET_MODULE_ABI_VERSION;

/// What a module does with one packet.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Verdict {
    /// Pass the packet to the next pipeline stage.
    Output,
    /// Drop the packet.
    Drop,
}

/// A dataplane module.
pub trait Module {
    /// Mirror of the module's own configuration fields, after the C module
    /// header; the module's C header is the source of truth for its layout.
    type Config: FromBytes + KnownLayout;

    /// Per-invocation state derived from the configuration, such as views.
    type Views<'g>;

    /// Derives the per-invocation state once per handler invocation.
    fn attach<'g>(config: &ConfigView<'g, Self::Config>) -> Self::Views<'g>;

    /// Decides the fate of one packet.
    ///
    /// Runs on a worker with exclusive access to the packet; the
    /// configuration is read-only and stays published for the call.
    fn handle_packet(views: &Self::Views<'_>, packet: &mut Packet<'_>) -> Verdict;
}

/// C handler trampoline for module `M`.
///
/// # Safety
///
/// Called only by the dataplane with a published execution context whose
/// configuration has the C layout the module's configuration mirrors and was
/// validated before publication, and with the invocation's own front of
/// parsed packets on live mbufs.
#[doc(hidden)]
pub unsafe extern "C" fn handle_packets<M: Module>(
    _dp_worker: *mut dp_worker,
    module_ectx: *mut module_ectx,
    packet_front: *mut packet_front,
) {
    // SAFETY: the dataplane passes a live context; the absolute module
    // pointer is a raw pointer written by the publishing process, carrying
    // the mapping's provenance per the crate contract.
    let config = unsafe { (&raw const (*module_ectx).abs_cp_module).read() };
    let root = NonNull::new(config.cast::<u8>()).expect("a published context has a module configuration");
    let front = NonNull::new(packet_front).expect("the dataplane passes a front");
    // SAFETY: see the function contract; the configuration stays published
    // for the call and the front belongs to this invocation.
    let (config, mut front) = unsafe {
        (
            ConfigView::<M::Config>::attach(Root::from_raw(root)),
            Front::from_raw(front),
        )
    };
    let views = M::attach(&config);
    while let Some(mut packet) = front.pop_input() {
        match M::handle_packet(&views, &mut packet) {
            Verdict::Output => front.output(packet),
            Verdict::Drop => front.drop_packet(packet),
        }
    }
}

/// Allocates the module descriptor the loader copies and frees.
///
/// Returns NULL when the allocation fails or the name does not fit.
#[doc(hidden)]
pub fn new_module<M: Module>(name: &str) -> *mut module {
    unsafe extern "C" {
        fn calloc(count: usize, size: usize) -> *mut core::ffi::c_void;
    }
    if name.len() >= MODULE_TYPE_LEN {
        return ptr::null_mut();
    }
    // SAFETY: plain allocation; the loader releases it with `free`.
    let raw = unsafe { calloc(1, size_of::<module>()) }.cast::<module>();
    let Some(descriptor) = NonNull::new(raw) else {
        return ptr::null_mut();
    };
    // SAFETY: the block is zeroed, sized and aligned for a descriptor, and
    // all-zero is a valid descriptor (NUL name, no handlers).
    let descriptor = unsafe { &mut *descriptor.as_ptr() };
    for (dst, &src) in descriptor.name.iter_mut().zip(name.as_bytes()) {
        *dst = src as c_char;
    }
    // No commit hooks: the configuration needs no dataplane-side derivation,
    // and the loader skips absent hooks.
    descriptor.handler = Some(handle_packets::<M>);
    raw
}

/// Whether two names are equal, usable in constant evaluation.
#[doc(hidden)]
pub const fn same_name(a: &str, b: &str) -> bool {
    let (a, b) = (a.as_bytes(), b.as_bytes());
    if a.len() != b.len() {
        return false;
    }
    let mut idx = 0;
    while idx < a.len() {
        if a[idx] != b[idx] {
            return false;
        }
        idx += 1;
    }
    true
}

/// Compiles only when both arguments have the same type.
#[doc(hidden)]
pub const fn same_type<T>(_: core::marker::PhantomData<T>, _: core::marker::PhantomData<T>) {}

/// Exports module `$module` as `new_module_$name` plus the ABI version.
///
/// Accepts only an identifier and a path, so the expansion carries no code
/// from the module crate; the generated items call safe SDK functions, and
/// the `unsafe(export_name)` attributes are the only unsafe tokens. Nothing
/// at run time ties the exported name to the configuration mirror, so the
/// export also publishes the name and the module type for the module's
/// systest to check against the C header.
#[macro_export]
macro_rules! export_module {
    ($name:ident, $module:path) => {
        /// Module type name this crate exports, checked by its systest.
        #[doc(hidden)]
        pub const YANET_MODULE_NAME: &str = stringify!($name);

        /// Exported module type, checked by its systest.
        #[doc(hidden)]
        pub type YanetModule = $module;

        const _: () = {
            assert!(
                stringify!($name).len() < $crate::sys::ffi::MODULE_TYPE_LEN,
                "module name does not fit the descriptor"
            );

            #[unsafe(export_name = concat!("new_module_", stringify!($name)))]
            extern "C" fn new_module() -> *mut $crate::sys::ffi::module {
                $crate::new_module::<$module>(stringify!($name))
            }

            #[unsafe(export_name = "yanet_module_abi_version")]
            static ABI_VERSION: u32 = $crate::ABI_VERSION;
        };
    };
}

#[cfg(test)]
mod tests {
    use core::ptr::{self, NonNull};

    use yanet_sys::{
        ConfigView, Lpm, LpmView,
        ffi::{
            RTE_ETHER_TYPE_IPV4, YANET_RS_MBUF_BUF_ADDR_OFFSET, YANET_RS_MBUF_DATA_LEN_OFFSET,
            YANET_RS_MBUF_DATA_OFF_OFFSET, module_ectx, packet, packet_front, packet_list,
        },
    };
    use yanet_testkit::{RawBuf, fixture};
    use zerocopy::{FromBytes, KnownLayout};

    use super::{Module, Packet, Verdict, handle_packets, new_module};

    /// Body of the C-built fixture image: two trees after the module header.
    #[derive(FromBytes, KnownLayout)]
    #[repr(C)]
    struct TwoTrees {
        first: Lpm,
        second: Lpm,
    }

    /// Drops IPv4 packets whose destination is in the first tree; the
    /// fixture's first tree holds IPv4 prefixes.
    enum Probe {}

    impl Module for Probe {
        type Config = TwoTrees;
        type Views<'g> = [LpmView<'g>; 2];

        fn attach<'g>(config: &ConfigView<'g, TwoTrees>) -> [LpmView<'g>; 2] {
            let body = config.body();
            [config.lpm(&body.first), config.lpm(&body.second)]
        }

        fn handle_packet([first, _]: &[LpmView<'_>; 2], packet: &mut Packet<'_>) -> Verdict {
            match packet.network_header::<20>() {
                Some(header) if first.contains(&[header[16], header[17], header[18], header[19]]) => Verdict::Drop,
                _ => Verdict::Output,
            }
        }
    }

    const HEADROOM: usize = 128;
    const NETWORK_OFFSET: u16 = 14;

    /// A packet whose mbuf and buffer live in one raw allocation, carrying an
    /// IPv4 header with destination `dst` at the network offset.
    struct TestPacket {
        buf: RawBuf,
    }

    impl TestPacket {
        fn new(dst: [u8; 4]) -> Self {
            let buf = RawBuf::zeroed(1024);
            let mbuf = buf.as_ptr();
            // SAFETY: all writes stay inside the allocation; the layout is a
            // 256-byte mbuf stand-in followed by the buffer, whose start holds
            // the packet descriptor as on the receive path.
            unsafe {
                let data_buf = mbuf.add(256);
                mbuf.add(YANET_RS_MBUF_BUF_ADDR_OFFSET)
                    .cast::<*mut u8>()
                    .write(data_buf);
                mbuf.add(YANET_RS_MBUF_DATA_OFF_OFFSET)
                    .cast::<u16>()
                    .write(HEADROOM as u16);
                mbuf.add(YANET_RS_MBUF_DATA_LEN_OFFSET)
                    .cast::<u16>()
                    .write(NETWORK_OFFSET + 40);
                let header = data_buf.add(HEADROOM + usize::from(NETWORK_OFFSET));
                header.write(0x45);
                ptr::copy_nonoverlapping(dst.as_ptr(), header.add(16), 4);
                let packet = data_buf.cast::<packet>();
                (*packet).mbuf = mbuf.cast();
                (*packet).data_len = NETWORK_OFFSET + 40;
                (*packet).network_header.r#type = RTE_ETHER_TYPE_IPV4.to_be();
                (*packet).network_header.offset = NETWORK_OFFSET;
            }
            Self { buf }
        }

        fn packet(&self) -> *mut packet {
            // SAFETY: the descriptor lies inside the allocation.
            unsafe { self.buf.as_ptr().add(256).cast() }
        }
    }

    /// Packets of `list` in order.
    fn collect(list: &packet_list) -> Vec<*mut packet> {
        let mut out = Vec::new();
        let mut cursor = list.first;
        while !cursor.is_null() {
            out.push(cursor);
            // SAFETY: list members are live test packets.
            cursor = unsafe { (*cursor).next };
        }
        out
    }

    /// Verifies that the trampoline routes every input packet to output or
    /// drop by the module's verdict, with the configuration resolved from the
    /// execution context; Miri checks the raw list and mbuf accesses.
    #[test]
    fn test_trampoline_routes_packets_by_verdict() {
        let image = RawBuf::from_bytes(fixture::IMAGE);
        let config = image.ptr_at(fixture::CONFIG_OFFSET);
        let (matching, _) = fixture::keys4()
            .find(|&(_, value)| value != yanet_sys::ffi::LPM_VALUE_INVALID)
            .expect("the fixture has a matching key");
        let packets = [
            TestPacket::new(matching),
            TestPacket::new([0, 0, 0, 1]),
            TestPacket::new(matching),
        ];

        let ectx_buf = RawBuf::zeroed(size_of::<module_ectx>());
        let ectx = ectx_buf.as_ptr().cast::<module_ectx>();
        let front_buf = RawBuf::zeroed(size_of::<packet_front>());
        let front = front_buf.as_ptr().cast::<packet_front>();
        // SAFETY: both structures are zeroed raw allocations of their size;
        // the input list is linked by hand like the C front input helper.
        unsafe {
            (*ectx).abs_cp_module = config.as_ptr().cast();
            let mut last: *mut *mut packet = &raw mut (*front).input.first;
            for test_packet in &packets {
                *last = test_packet.packet();
                last = &raw mut (*test_packet.packet()).next;
            }
            (*front).input.last = last;
            handle_packets::<Probe>(ptr::null_mut(), ectx, front);
        }

        // SAFETY: the front is live and no longer mutated.
        let front = unsafe { &*front };
        assert!(front.input.first.is_null());
        assert_eq!(vec![packets[1].packet()], collect(&front.output));
        assert_eq!(vec![packets[0].packet(), packets[2].packet()], collect(&front.drop));
        assert_eq!((1, 2), (front.output_count, front.drop_count));
        assert_eq!(u64::from(NETWORK_OFFSET + 40) * 2, front.drop_bytes);
    }

    #[test]
    fn test_new_module_fills_descriptor() {
        let raw = NonNull::new(new_module::<Probe>("probe")).expect("allocation succeeds");
        // SAFETY: a fresh calloc'ed descriptor, released below.
        let descriptor = unsafe { raw.as_ref() };
        assert_eq!(
            b"probe\0",
            &descriptor.name[..6].iter().map(|&c| c as u8).collect::<Vec<_>>()[..]
        );
        assert!(descriptor.handler.is_some());
        assert!(descriptor.commit_handler.is_none() && descriptor.commit_ectx_handler.is_none());
        unsafe extern "C" {
            fn free(ptr: *mut core::ffi::c_void);
        }
        // SAFETY: the loader contract: the descriptor is a calloc block.
        unsafe { free(raw.as_ptr().cast()) };
    }

    #[test]
    fn test_new_module_rejects_overlong_name() {
        assert!(new_module::<Probe>(&"x".repeat(80)).is_null());
    }
}
