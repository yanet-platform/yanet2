//! Safe execution-context access: config, counters, routing, prepared
//! state, worker services.

use core::marker::PhantomData;

use yanet_shm::{CpModule, ModuleConfig, ObjectConfig, counter_get_address};

use crate::{front::packet_list_add, packet::Packet, raw};

/// A counter values array, single-writer per worker.
///
/// Size-2 counters lay out as `[packets, bytes]`; larger counters follow
/// their registered schema.
#[derive(Clone, Copy)]
pub struct Counter {
    values: *mut u64,
}

impl Counter {
    /// Read one slot.
    ///
    /// # Panics
    ///
    /// Panics when `slot` is 3 or more: counters hold at most two slots.
    pub fn get(&self, slot: usize) -> u64 {
        assert!(slot < 2, "a counter holds at most two slots");
        // SAFETY: the storage owns slot_count live u64s and module code
        // reads within its registered schema.
        unsafe { *self.values.add(slot) }
    }

    /// Add `delta` to one slot.
    ///
    /// # Panics
    ///
    /// Panics when `slot` is 3 or more: counters hold at most two slots.
    pub fn add(&mut self, slot: usize, delta: u64) {
        assert!(slot < 2, "a counter holds at most two slots");
        // SAFETY: same live-values contract as get().
        unsafe { *self.values.add(slot) += delta };
    }

    /// Count one packet of `bytes` first-segment length: packets and
    /// bytes slots together.
    pub fn add_packet(&mut self, bytes: u16) {
        self.add(0, 1);
        self.add(1, bytes as u64);
    }
}

/// A module's per-worker execution context, one module call wide.
pub struct Ectx<'a> {
    worker: *mut raw::DpWorker,
    raw: *mut raw::ModuleEctx,
    _marker: PhantomData<&'a mut ()>,
}

impl<'a> Ectx<'a> {
    /// Wrap the pair the dataplane trampoline received.
    ///
    /// # Safety
    ///
    /// Both pointers must be live for the module call and belong to the
    /// same worker round.
    pub(crate) unsafe fn new(worker: *mut raw::DpWorker, raw: *mut raw::ModuleEctx) -> Self {
        debug_assert!(!worker.is_null());
        debug_assert!(!raw.is_null());
        Self { worker, raw, _marker: PhantomData }
    }

    /// Wrap a context for a commit-ectx hook, which carries no worker.
    ///
    /// # Safety
    ///
    /// `raw` must be live for the hook; worker services stay
    /// unavailable through this context.
    pub(crate) unsafe fn new_for_commit(raw: *mut raw::ModuleEctx) -> Self {
        debug_assert!(!raw.is_null());
        Self {
            worker: core::ptr::null_mut(),
            raw,
            _marker: PhantomData,
        }
    }

    fn worker(&self) -> *mut raw::DpWorker {
        assert!(
            !self.worker.is_null(),
            "worker services are only available in the packet handler"
        );
        self.worker
    }

    fn raw(&self) -> &raw::ModuleEctx {
        // SAFETY: built only by the trampoline around a live context.
        unsafe { &*self.raw }
    }

    /// This module's typed configuration, as published by the control
    /// plane for the current generation.
    pub fn config<T: ModuleConfig>(&self) -> &'a T {
        let cp_module = self.raw().abs_cp_module as *const CpModule;
        // SAFETY: the context's module pointer names this module's own
        // config, and T is the module's own config type.
        unsafe { (*cp_module).container_of::<T>() }
    }

    /// Resolve a counter from the module's registry by id.
    pub fn counter(&self, id: u64) -> Option<Counter> {
        // SAFETY: the absolutization pass filled abs_counter_storage
        // before workers saw the context.
        let storage = unsafe { self.raw().abs_counter_storage.as_ref() }?;
        Some(Counter {
            // SAFETY: id comes from the module's own registry, which the
            // per-worker storage spawned in full.
            values: counter_get_address(storage, id),
        })
    }

    /// The framework's rx counter for this module.
    pub fn rx_counter(&self) -> Counter {
        self.framework_counter(0)
    }

    /// The framework's tx counter for this module.
    pub fn tx_counter(&self) -> Counter {
        self.framework_counter(1)
    }

    /// The framework's drop counter for this module.
    pub fn drop_counter(&self) -> Counter {
        self.framework_counter(2)
    }

    /// The framework's pending-input counter for this module.
    pub fn pending_input_counter(&self) -> Counter {
        self.framework_counter(3)
    }

    /// The framework's pending-output counter for this module.
    pub fn pending_output_counter(&self) -> Counter {
        self.framework_counter(4)
    }

    fn framework_counter(&self, idx: usize) -> Counter {
        let raw = self.raw();
        let value = [
            raw.rx_counter,
            raw.tx_counter,
            raw.drop_counter,
            raw.pending_input_counter,
            raw.pending_output_counter,
        ][idx];
        // SAFETY: the absolutization pass derived all five from the
        // module's registry ids before workers saw the context.
        Counter { values: value }
    }

    /// Resolve a counter from one of the module's runtime (named)
    /// registries.
    pub fn runtime_counter(&self, registry: usize, id: u64) -> Option<Counter> {
        let raw = self.raw();
        if registry >= raw.runtime_counter_storage_count as usize {
            return None;
        }
        // SAFETY: the absolutization pass filled the absolute storages
        // array in parallel with the registry list.
        let storage = unsafe {
            let array = raw.abs_runtime_counter_storages_base;
            *array.add(registry)
        };
        if storage.is_null() {
            return None;
        }
        Some(Counter {
            // SAFETY: id comes from the named registry that spawned this
            // storage.
            values: counter_get_address(unsafe { &*storage }, id),
        })
    }

    /// The module's private per-worker buffer, `None` when the module
    /// declared no prepared size.
    pub fn prepared<T>(&self) -> Option<&'a mut T> {
        let buffer = self.raw().abs_module_prepared;
        if buffer.is_null() {
            return None;
        }
        // SAFETY: the buffer is prepared_size bytes of zeroed memory the
        // module owns for this worker, and T is its declared type.
        Some(unsafe { &mut *(buffer as *mut T) })
    }

    /// The redirect budget of packet lineages on this module.
    pub fn recirc_limit(&self) -> u16 {
        self.raw().packet_recirc_limit
    }

    /// The worker index this context executes on.
    pub fn worker_idx(&self) -> u64 {
        // SAFETY: the checked accessor holds a live trampoline worker.
        unsafe { (*self.worker()).idx }
    }

    /// The worker's current time in nanoseconds, sampled at round start.
    pub fn now_ns(&self) -> u64 {
        // SAFETY: same live worker.
        unsafe { (*self.worker()).current_time }
    }

    /// Allocate a fresh packet from this worker's pool.
    pub fn alloc_packet(&self) -> Option<Packet<'a>> {
        Packet::alloc(self.worker())
    }

    /// Deep-copy a packet, partitioning the redirect credits between the
    /// copies.
    pub fn clone_packet(&self, packet: &Packet<'a>) -> Option<Packet<'a>> {
        Packet::clone_from(self.worker(), packet, self.recirc_limit())
    }

    /// Free a packet this module allocated or cloned, when it no longer
    /// needs a counted push anywhere.
    pub fn free_packet(&self, packet: Packet<'a>) {
        packet.free();
    }

    /// The routing target of the module device at `index`, `None` when
    /// out of range or absent from this generation.
    pub fn device_target(&self, index: usize) -> Option<DeviceTarget<'a>> {
        let raw = self.raw();
        if index >= raw.device_target_count as usize {
            return None;
        }
        // SAFETY: the absolutization pass filled the absolute target
        // table before workers saw the context.
        let target = unsafe { &*raw.abs_device_targets.add(index) };
        if target.device_id == u16::MAX {
            return None;
        }
        Some(DeviceTarget { raw: target })
    }

    /// The object link at `index`, `None` when out of range.
    pub fn object_link(&self, index: usize) -> Option<ObjectLink<'a>> {
        let raw = self.raw();
        if index >= raw.object_link_count as usize {
            return None;
        }
        // SAFETY: the absolutization pass filled the absolute link array
        // before workers saw the context.
        let link = unsafe { &*raw.abs_object_links.add(index) };
        Some(ObjectLink { raw: link })
    }

    /// Route a packet to a target device's input entry, mirroring
    /// `module_ectx_route_input`.
    ///
    /// The packet's tx device must already name the destination; an
    /// exhausted redirect budget drops the packet on `front` instead.
    pub fn route_input(&self, front: &mut crate::PacketFront<'a>, target: DeviceTarget<'a>, mut packet: Packet<'a>) {
        let raw = self.raw();
        let packet_len = packet.len();
        let front_raw = front.raw_mut();
        front_raw.pending_input_count += 1;
        front_raw.pending_input_bytes += packet_len as u64;
        if packet.recirc_try_redirect(raw.packet_recirc_limit) {
            // SAFETY: the generation and entry pointers are live for the
            // round; the packet is unlinked and owned here.
            unsafe {
                schedule_on_ready(raw.abs_config_gen_ectx, target.input_entry(), packet);
            }
        } else {
            // SAFETY: same liveness; the front borrows are exclusive.
            unsafe {
                count_recirc_drop(target.input_entry(), &packet);
                push_drop(front_raw, packet.as_raw());
            }
        }
    }

    /// Route a packet to a target device's output entry, mirroring
    /// `module_ectx_route_output`.
    pub fn route_output(&self, front: &mut crate::PacketFront<'a>, target: DeviceTarget<'a>, mut packet: Packet<'a>) {
        let raw = self.raw();
        let packet_len = packet.len();
        let front_raw = front.raw_mut();
        front_raw.pending_output_count += 1;
        front_raw.pending_output_bytes += packet_len as u64;
        if packet.recirc_try_redirect(raw.packet_recirc_limit) {
            // SAFETY: the generation and entry pointers are live for the
            // round; the packet is unlinked and owned here.
            unsafe {
                schedule_on_ready(raw.abs_config_gen_ectx, target.output_entry(), packet);
            }
        } else {
            // SAFETY: same liveness; the front borrows are exclusive.
            unsafe {
                count_recirc_drop(target.output_entry(), &packet);
                push_drop(front_raw, packet.as_raw());
            }
        }
    }
}

/// A module device's routing target: the generation device id plus the
/// device's two pipeline entries.
pub struct DeviceTarget<'a> {
    raw: &'a raw::ModuleDeviceTarget,
}

impl<'a> DeviceTarget<'a> {
    /// The generation-global device id.
    pub fn device_id(&self) -> u16 {
        self.raw.device_id
    }

    fn input_entry(&self) -> *mut raw::DeviceEntryEctx {
        self.raw.abs_input_entry
    }

    fn output_entry(&self) -> *mut raw::DeviceEntryEctx {
        self.raw.abs_output_entry
    }
}

/// A module's per-worker link to a shared object.
pub struct ObjectLink<'a> {
    raw: &'a raw::ModuleObjectLinkEctx,
}

impl<'a> ObjectLink<'a> {
    /// Resolve a counter from the linked object's link registry.
    pub fn counter(&self, id: u64) -> Option<Counter> {
        let storage = self.raw.counter_storage;
        if storage.is_null() {
            return None;
        }
        Some(Counter {
            // SAFETY: id comes from the link registry that spawned this
            // per-link storage.
            values: counter_get_address(unsafe { &*storage }, id),
        })
    }

    /// The linked object as its concrete type, `None` before the
    /// absolutization pass.
    pub fn object<T: ObjectConfig>(&self) -> Option<&'a T> {
        let ectx = self.raw.abs_object_ectx;
        if ectx.is_null() {
            return None;
        }
        let cp_object = unsafe { (*ectx).abs_cp_object };
        if cp_object.is_null() {
            return None;
        }
        // SAFETY: the link names one object of one type, and T is the
        // module's declared object type for this link.
        Some(unsafe { (*cp_object).container_of::<T>() })
    }
}

/// Mirror of `device_entry_ectx_schedule`.
///
/// # Safety
///
/// `entry` must be a live entry of the generation `config_gen_ectx`.
unsafe fn schedule_on_ready(
    config_gen_ectx: *mut raw::ConfigGenEctx,
    entry: *mut raw::DeviceEntryEctx,
    packet: Packet<'_>,
) {
    // SAFETY: caller guarantees live structures; the rlist links are
    // worker-local by contract.
    unsafe {
        if (*entry).schedule_list != raw::DEVICE_ENTRY_SCHEDULE_READY {
            rlist_remove(&mut (*entry).schedule_node);
            rlist_add(&mut (*config_gen_ectx).ready_list, &mut (*entry).schedule_node);
            (*entry).schedule_list = raw::DEVICE_ENTRY_SCHEDULE_READY;
        }
        // The entry's schedule front counts the packet as input.
        packet_list_add(&mut (*entry).schedule.input, packet.as_raw());
        (*entry).schedule.input_count += 1;
        (*entry).schedule.input_bytes += (*packet.as_raw()).data_len as u64;
    }
}

/// Mirror of `device_entry_ectx_count_recirc_drop`.
unsafe fn count_recirc_drop(entry: *mut raw::DeviceEntryEctx, packet: &Packet<'_>) {
    // SAFETY: the entry is live and its recirc-drop handle was derived
    // before workers saw the generation.
    unsafe {
        let counter = (*entry).counter_packet_recirc_drop;
        *counter += 1;
        *counter.add(1) += packet.total_len() as u64;
    }
}

/// Push a packet onto a front's drop list with its byte accounting.
///
/// # Safety
///
/// `packet` must be unlinked and owned by the caller.
unsafe fn push_drop(front: &mut raw::PacketFront, packet: *mut raw::Packet) {
    // SAFETY: the front is live for the module call.
    unsafe {
        packet_list_add(&mut front.drop, packet);
        front.drop_count += 1;
        front.drop_bytes += (*packet).data_len as u64;
    }
}

/// Mirror of `rlist_remove`.
unsafe fn rlist_remove(node: *mut raw::Rlist) {
    // SAFETY: the node is linked on one of the generation's worklists.
    unsafe {
        let prev = (*node).prev;
        let next = (*node).next;
        (*prev).next = next;
        (*next).prev = prev;
    }
}

/// Mirror of `rlist_add` (append at tail).
unsafe fn rlist_add(head: *mut raw::Rlist, node: *mut raw::Rlist) {
    // SAFETY: both nodes are live and the head owns its list.
    unsafe {
        (*node).prev = (*head).prev;
        (*node).next = head;
        (*(*head).prev).next = node;
        (*head).prev = node;
    }
}
