//! Strict-provenance checks of the shared-memory resolution, run under Miri.
//!
//! A fixture arena stands in for the shared mapping: one allocation holding a
//! device execution context and a Rust device item whose relative slot links
//! them, as the control plane lays them out. Every pointer into the arena
//! derives from the one allocation pointer, as the pointer C passes to a
//! handler does.

use core::{
    alloc::Layout,
    ffi::{CStr, c_void},
    mem::{offset_of, size_of},
    ptr::{self, NonNull},
};

use yanet_shm::ShmLayout;

use super::{Device, DeviceItem, Packet, Verdict, device_item, new_device, raw, resolve_rel};

unsafe extern "C" {
    fn free(ptr: *mut c_void);
}

// The native test binary links the handlers, which reference the C shim the
// dataplane build provides; these stand-ins satisfy the link and are never
// reached by a test.
mod shim {
    use super::raw;

    #[unsafe(no_mangle)]
    extern "C" fn yanet_dp_shim_packet_front_pop_input(_front: *mut raw::packet_front) -> *mut raw::packet {
        core::ptr::null_mut()
    }

    #[unsafe(no_mangle)]
    extern "C" fn yanet_dp_shim_packet_front_output(_front: *mut raw::packet_front, _packet: *mut raw::packet) {
        unreachable!()
    }

    #[unsafe(no_mangle)]
    extern "C" fn yanet_dp_shim_packet_front_drop(_front: *mut raw::packet_front, _packet: *mut raw::packet) {
        unreachable!()
    }

    #[unsafe(no_mangle)]
    extern "C" fn yanet_dp_shim_packet_data(_packet: *mut raw::packet, _len: *mut u16) -> *mut u8 {
        unreachable!()
    }

    #[unsafe(no_mangle)]
    extern "C" fn yanet_dp_shim_packet_len(_packet: *mut raw::packet) -> u32 {
        unreachable!()
    }

    #[unsafe(no_mangle)]
    extern "C" fn yanet_dp_shim_packet_prepend(_packet: *mut raw::packet, _len: u16) -> *mut u8 {
        unreachable!()
    }

    #[unsafe(no_mangle)]
    extern "C" fn yanet_dp_shim_packet_adj(_packet: *mut raw::packet, _len: u16) -> i32 {
        unreachable!()
    }

    #[unsafe(no_mangle)]
    extern "C" fn yanet_dp_shim_packet_parse(_packet: *mut raw::packet) -> i32 {
        unreachable!()
    }
}

#[derive(ShmLayout, Clone, Copy, Debug, PartialEq, Eq)]
#[repr(C)]
struct TestConfig {
    tag: u32,
    value: u64,
}

struct TestDevice;

impl Device for TestDevice {
    const NAME: &'static str = "test";
    type Config = TestConfig;

    fn input(_config: &TestConfig, _packet: &mut Packet<'_>) -> Verdict {
        Verdict::Output
    }

    fn output(_config: &TestConfig, _packet: &mut Packet<'_>) -> Verdict {
        Verdict::Drop
    }
}

struct LongNameDevice;

impl Device for LongNameDevice {
    const NAME: &'static str = concat!(
        "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
        "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
    );
    type Config = u64;

    fn input(_config: &u64, _packet: &mut Packet<'_>) -> Verdict {
        Verdict::Output
    }

    fn output(_config: &u64, _packet: &mut Packet<'_>) -> Verdict {
        Verdict::Output
    }
}

const ARENA_SIZE: usize = 8192;
const ECTX_OFFSET: usize = 64;
const DEVICE_OFFSET: usize = 4096;

/// A zeroed arena standing in for the shared mapping, freed on drop.
struct Arena {
    base: NonNull<u8>,
}

impl Arena {
    fn layout() -> Layout {
        Layout::from_size_align(ARENA_SIZE, 64).expect("valid arena layout")
    }

    fn new() -> Self {
        // SAFETY: the layout has a nonzero size.
        let base = unsafe { std::alloc::alloc_zeroed(Self::layout()) };
        Self {
            base: NonNull::new(base).expect("arena allocation"),
        }
    }

    fn at(&self, offset: usize) -> NonNull<u8> {
        assert!(offset < ARENA_SIZE);
        // SAFETY: the offset is in bounds of the arena.
        unsafe { self.base.add(offset) }
    }

    /// Places a context whose relative device slot points at a device item
    /// with the given body, and returns the context pointer.
    fn link_device(&self, config: TestConfig) -> NonNull<raw::device_ectx> {
        assert!(DEVICE_OFFSET + size_of::<DeviceItem<TestConfig>>() <= ARENA_SIZE);
        let ectx = self.at(ECTX_OFFSET).cast::<raw::device_ectx>();
        let slot = self.at(ECTX_OFFSET + offset_of!(raw::device_ectx, cp_device));
        let offset = DEVICE_OFFSET - (ECTX_OFFSET + offset_of!(raw::device_ectx, cp_device));
        let body_at = self.at(DEVICE_OFFSET + offset_of!(DeviceItem<TestConfig>, body));
        // SAFETY: every write is in bounds and aligned inside the arena.
        unsafe {
            slot.cast::<usize>().write(offset);
            body_at.cast::<TestConfig>().write(config);
        }
        ectx
    }
}

impl Drop for Arena {
    fn drop(&mut self) {
        // SAFETY: allocated in the constructor with the same layout.
        unsafe { std::alloc::dealloc(self.base.as_ptr(), Self::layout()) };
    }
}

const CONFIG: TestConfig = TestConfig {
    tag: 0x00ab_cdef,
    value: u64::MAX - 1,
};

/// Resolves the body of the device linked from the context, if accepted.
fn resolve(ectx: NonNull<raw::device_ectx>) -> Option<TestConfig> {
    // SAFETY: the arena holds a linked context and a frozen device.
    unsafe { device_item::<TestDevice>(ectx) }.map(|item| item.body)
}

#[test]
fn test_device_item_resolves_body_past_header() {
    let arena = Arena::new();
    let ectx = arena.link_device(CONFIG);

    assert_eq!(Some(CONFIG), resolve(ectx));
}

#[test]
fn test_device_item_null_slot_is_none() {
    let arena = Arena::new();
    let ectx = arena.at(ECTX_OFFSET).cast::<raw::device_ectx>();

    assert_eq!(None, resolve(ectx));
}

#[test]
fn test_resolve_rel_keeps_root_provenance_for_backward_offset() {
    let arena = Arena::new();
    let root = arena.at(0);
    let slot = arena.at(DEVICE_OFFSET).cast::<usize>();
    let target = arena.at(ECTX_OFFSET);
    // SAFETY: the slot is in bounds and aligned.
    unsafe { slot.as_ptr().write(target.addr().get().wrapping_sub(slot.addr().get())) };

    // SAFETY: the slot is readable and the root covers the arena.
    let resolved = unsafe { resolve_rel::<u8>(root, slot.as_ptr()) }.expect("non-null slot");
    // SAFETY: the target is in bounds of the arena.
    unsafe { resolved.as_ptr().write(7) };

    assert_eq!(target, resolved);
}

/// verifies that a root taken from a reference to the context alone cannot
/// reach the device: the reference covers only the context bytes.
///
/// Under Miri with Stacked Borrows this test must fail with undefined
/// behavior, which is why it is ignored by default.
#[test]
#[ignore = "expected to be rejected by Miri under Stacked Borrows"]
fn test_device_item_narrow_root_is_rejected() {
    let arena = Arena::new();
    let ectx = arena.link_device(CONFIG);
    // SAFETY: the context is in bounds, aligned and initialized.
    let narrow = NonNull::from(unsafe { ectx.as_ref() });

    assert_eq!(Some(CONFIG), resolve(narrow));
}

#[test]
fn test_new_device_fills_descriptor() {
    let device = new_device::<TestDevice>();
    assert!(!device.is_null());

    // SAFETY: the constructor returned an initialized descriptor.
    let (name, has_handlers, layout) = unsafe {
        let device = &*device;
        (
            CStr::from_ptr(device.name.as_ptr()).to_bytes().to_vec(),
            device.input_handler.is_some() && device.output_handler.is_some() && device.commit_handler.is_some(),
            device.config_layout,
        )
    };
    // SAFETY: allocated with malloc by the constructor.
    unsafe { free(device.cast()) };

    assert_eq!(b"test".to_vec(), name);
    assert!(has_handlers);
    assert_eq!(TestConfig::FINGERPRINT, layout);
}

#[test]
fn test_new_device_truncates_long_name() {
    let device = new_device::<LongNameDevice>();
    assert!(!device.is_null());

    // SAFETY: the constructor returned an initialized descriptor.
    let len = unsafe { CStr::from_ptr((*device).name.as_ptr()).to_bytes().len() };
    // SAFETY: allocated with malloc by the constructor.
    unsafe { free(device.cast()) };

    assert_eq!(raw::DEVICE_TYPE_LEN as usize - 1, len);
}

#[test]
fn test_input_handler_runs_on_empty_front() {
    let arena = Arena::new();
    let ectx = arena.link_device(CONFIG);
    let device = new_device::<TestDevice>();
    assert!(!device.is_null());

    // SAFETY: the handler gets a linked context; the stand-in front is
    // empty, so it resolves the device and returns.
    unsafe {
        let input = (*device).input_handler.expect("input handler");
        input(ptr::null_mut(), ectx.as_ptr(), ptr::null_mut());
        free(device.cast());
    }
}
