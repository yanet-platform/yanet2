//! Final-address native compilation from immutable borrowed attribute views.

use core::{
    marker::PhantomData,
    mem::{forget, size_of},
    ops::Range,
    ptr::{self, NonNull},
};

use super::{
    ByteLine, Decode, Device, Fragment, Graph, IpFrag, Joint, Net4, Net6, Port, RuleTable, Shape, Slot, Spec,
    records::{LineRange, Net4 as Network4, Net6 as Network6, PortRange},
    release_nodes, sealed, valid_net4, valid_net6,
};
use crate::{
    bindings,
    cp::{BuildError, Encode, MemoryContext, Owner, Refusal, Writer},
};

pub fn classify<S: Shape, I>(rule_count: usize, nodes: I) -> Classify<S, I> {
    Classify {
        rule_count,
        nodes,
        shape: PhantomData,
    }
}

pub struct Classify<S, I> {
    rule_count: usize,
    nodes: I,
    shape: PhantomData<fn() -> S>,
}

impl<S, I> Classify<S, I> {
    pub fn with_devices<'src, E, F>(self, len: usize, read: F) -> WithDevices<S, I, F>
    where
        F: FnMut(usize) -> Result<&'src str, E>,
    {
        WithDevices { graph: self, len, read }
    }
}
pub struct WithDevices<S, I, F> {
    graph: Classify<S, I>,
    len: usize,
    read: F,
}

pub fn net4<'src, E, F>(read: F) -> Net4Init<F>
where
    F: FnMut(usize) -> Result<Option<&'src [Network4]>, E>,
{
    Net4Init(read)
}
pub struct Net4Init<F>(F);
pub fn joint() -> JointInit {
    JointInit
}
pub struct JointInit;
pub fn decode<E, F>(present: F) -> DecodeInit<F>
where
    F: FnMut(usize) -> Result<bool, E>,
{
    DecodeInit(present, false)
}
pub struct DecodeInit<F>(F, bool);

struct Scratch<T>(NonNull<T>);
impl<T> Scratch<T> {
    fn new(count: usize) -> Result<Self, Refusal> {
        count
            .max(1)
            .checked_mul(size_of::<T>())
            .ok_or(Refusal::InvalidClassifyInput)?;
        // SAFETY: native calloc accepts the checked layout and returns aligned,
        // zeroed storage retained until this guard drops.
        NonNull::new(unsafe { bindings::yanet_sys_classify_alloc(count.max(1), size_of::<T>()) }.cast())
            .map(Self)
            .ok_or(Refusal::OutOfMemory)
    }
    fn ptr(&self) -> *mut T {
        self.0.as_ptr()
    }
}
impl<T> Drop for Scratch<T> {
    fn drop(&mut self) {
        // SAFETY: this is the original calloc allocation, released exactly
        // once.
        unsafe { bindings::yanet_sys_classify_free(self.ptr().cast()) }
    }
}

struct Stages<'o, W: Owner> {
    storage: Scratch<bindings::classifier>,
    memory: MemoryContext<'o, W>,
    count: u32,
    visited: usize,
}
impl<W: Owner> Drop for Stages<'_, W> {
    fn drop(&mut self) {
        for index in (0..self.visited).rev() {
            // SAFETY: every visited stage started zeroed and retained its fixed
            // address. Finalization includes the failed current stage.
            unsafe {
                bindings::yanet_sys_classify_stage_free(
                    self.storage.ptr().add(index),
                    self.memory.native_context(),
                    self.count,
                )
            }
        }
    }
}

struct Output<'o, S: Shape, W: Owner> {
    destination: *mut Slot,
    memory: MemoryContext<'o, W>,
    shape: PhantomData<S>,
}
impl<S: Shape, W: Owner> Drop for Output<'_, S, W> {
    fn drop(&mut self) {
        // SAFETY: every slot retains its final address.
        //
        // Slots began zeroed; native destructors accept partial or failed
        // output.
        unsafe { release_nodes::<S>(self.destination, S::KINDS.len(), self.memory.native_context()) }
    }
}

/// Trusted state shared only by the sealed initializer implementations.
#[doc(hidden)]
pub struct CompileContext<'o, W: Owner> {
    stages: Stages<'o, W>,
    destination: *mut Slot,
    devices: Option<Scratch<bindings::filter_device>>,
    device_count: usize,
}

/// A closed tuple compiler whose successful outputs are complete native nodes.
///
/// # Safety
/// Every static shape position must be initialized at its final address, with
/// native resources left guarded on errors and unwinding.
#[doc(hidden)]
pub unsafe trait Initializers<S: Shape, E>: sealed::Init<S, E> {
    /// Completes the native nodes at their final static positions.
    ///
    /// # Safety
    ///
    /// The context must contain zeroed final slots matching the static shape.
    unsafe fn compile<W: Owner>(self, context: &mut CompileContext<'_, W>) -> Result<(), E>
    where
        E: From<BuildError<W::Error>>;
}

trait NodeInit<N: Spec, E> {
    unsafe fn compile<W: Owner>(self, index: usize, context: &mut CompileContext<'_, W>) -> Result<(), E>
    where
        E: From<BuildError<W::Error>>;
}

// SAFETY: the closed compiler consumes getters under graph and stage guards.
unsafe impl<S: Shape, I: Initializers<S, E>, E> Encode<E> for Classify<S, I> {
    type Output = Graph<S>;
    unsafe fn encode<W: Owner>(self, writer: &mut Writer<'_, W>, destination: *mut Graph<S>) -> Result<(), E>
    where
        E: From<BuildError<W::Error>>,
    {
        // SAFETY: the caller provides the complete exclusive final graph.
        unsafe { encode_graph(self, writer, destination, 0, |_| Ok("")) }
    }
}
// SAFETY: names are resolved through the encoding writer before native compile;
// the private scratch and every callback remain inside the armed graph guard.
unsafe impl<'src, S: Shape, I: Initializers<S, E>, E, F> Encode<E> for WithDevices<S, I, F>
where
    F: FnMut(usize) -> Result<&'src str, E>,
{
    type Output = Graph<S>;
    unsafe fn encode<W: Owner>(self, writer: &mut Writer<'_, W>, destination: *mut Graph<S>) -> Result<(), E>
    where
        E: From<BuildError<W::Error>>,
    {
        // SAFETY: the caller supplies the exclusive final graph destination.
        unsafe { encode_graph(self.graph, writer, destination, self.len, self.read) }
    }
}
unsafe fn encode_graph<'src, S: Shape, I: Initializers<S, E>, E, F, W: Owner>(
    input: Classify<S, I>,
    writer: &mut Writer<'_, W>,
    destination: *mut Graph<S>,
    device_count: usize,
    mut read: F,
) -> Result<(), E>
where
    F: FnMut(usize) -> Result<&'src str, E>,
    E: From<BuildError<W::Error>>,
{
    const { S::VALID };
    let count = u32::try_from(input.rule_count).map_err(|_| BuildError::Refused(Refusal::InvalidClassifyInput))?;
    let memory = writer.memory();
    // SAFETY: all nodes begin in the native destructors' empty state in place.
    unsafe { destination.cast::<u8>().write_bytes(0, size_of::<Graph<S>>()) };
    let output = Output::<S, W> {
        destination: destination.cast(),
        memory,
        shape: PhantomData,
    };
    let devices = if device_count == 0 {
        None
    } else {
        Some(Scratch::<bindings::filter_device>::new(device_count).map_err(BuildError::Refused)?)
    };
    for index in 0..device_count {
        let name = read(index)?;
        if name.len() >= 80 {
            return Err(BuildError::Refused(Refusal::InvalidClassifyInput).into());
        }
        let id = writer.link_device(name)?;
        if id >= u64::from(u32::MAX) {
            return Err(BuildError::Refused(Refusal::InvalidClassifyInput).into());
        }
        // SAFETY: checked bounds select one zeroed native record; the name fits
        // with its terminator and this header supplied the private native ID.
        unsafe {
            let value = devices.as_ref().expect("nonempty device scratch").ptr().add(index);
            ptr::copy_nonoverlapping(name.as_ptr(), ptr::addr_of_mut!((*value).name).cast(), name.len());
            ptr::addr_of_mut!((*value).id).write(id);
        }
    }
    drop(read);
    let mut context = CompileContext {
        stages: Stages {
            storage: Scratch::new(S::KINDS.len()).map_err(BuildError::Refused)?,
            memory,
            count,
            visited: 0,
        },
        destination: destination.cast(),
        devices,
        device_count,
    };
    // SAFETY: fixed stages and zeroed final slots match the sealed shape.
    unsafe { input.nodes.compile(&mut context)? };
    drop(context);
    forget(output);
    Ok(())
}

impl<'src, E, F> NodeInit<Net4, E> for Net4Init<F>
where
    F: FnMut(usize) -> Result<Option<&'src [Network4]>, E>,
{
    unsafe fn compile<W: Owner>(self, index: usize, context: &mut CompileContext<'_, W>) -> Result<(), E>
    where
        E: From<BuildError<W::Error>>,
    {
        let Net4Init(mut read) = self;
        let count = context.stages.count as usize;
        let views = Scratch::<bindings::yanet_sys_net4_input>::new(count).map_err(BuildError::Refused)?;
        let rules = Scratch::<*const bindings::classifier_rule>::new(count).map_err(BuildError::Refused)?;
        for rule in 0..count {
            let Some(networks) = read(rule)? else {
                continue;
            };
            if !networks.iter().all(valid_net4) {
                return Err(BuildError::Refused(Refusal::InvalidClassifyInput).into());
            }
            let len = u32::try_from(networks.len()).map_err(|_| BuildError::Refused(Refusal::InvalidClassifyInput))?;
            // SAFETY: descriptor and projection slots are inside their guarded
            // calloc allocations; borrowed, aligned networks outlive this call.
            unsafe {
                let view = views.ptr().add(rule);
                ptr::addr_of_mut!((*view).nets).write(bindings::filter_net4s {
                    items: if len == 0 {
                        ptr::null_mut()
                    } else {
                        networks.as_ptr().cast_mut().cast()
                    },
                    count: len,
                });
                rules.ptr().add(rule).write(view.cast());
            }
        }
        drop(read);
        // SAFETY: static C getters only copy immutable prepared views. Output
        // and stage remain guarded on every native failure path.
        let status = unsafe {
            bindings::yanet_sys_classify_net4_compile(
                context.stages.memory.native_context(),
                rules.ptr(),
                context.stages.count,
                context.destination.add(index).cast(),
                context.stages.storage.ptr().add(index),
            )
        };
        if status != 0 {
            return Err(BuildError::Refused(Refusal::OutOfMemory).into());
        }
        Ok(())
    }
}

impl<const L: usize, const R: usize, E> NodeInit<Joint<L, R>, E> for JointInit {
    unsafe fn compile<W: Owner>(self, index: usize, context: &mut CompileContext<'_, W>) -> Result<(), E>
    where
        E: From<BuildError<W::Error>>,
    {
        // SAFETY: static shape validation proves both initialized stage indices
        // precede this joint, which writes a zeroed final table and stage.
        let status = unsafe {
            bindings::classify_join(
                context.stages.memory.native_context(),
                c"joint".as_ptr(),
                context.stages.storage.ptr().add(L),
                context.stages.storage.ptr().add(R),
                context.stages.count,
                context.destination.add(index).cast(),
                context.stages.storage.ptr().add(index),
            )
        };
        if status != 0 {
            return Err(BuildError::Refused(Refusal::OutOfMemory).into());
        }
        Ok(())
    }
}

impl<const SOURCE: usize, E, F> NodeInit<Decode<SOURCE>, E> for DecodeInit<F>
where
    F: FnMut(usize) -> Result<bool, E>,
{
    unsafe fn compile<W: Owner>(self, index: usize, context: &mut CompileContext<'_, W>) -> Result<(), E>
    where
        E: From<BuildError<W::Error>>,
    {
        let DecodeInit(mut present, optional) = self;
        let count = context.stages.count as usize;
        let rules = Scratch::<*const bindings::classifier_rule>::new(count).map_err(BuildError::Refused)?;
        let mut any = false;
        for rule in 0..count {
            if present(rule)? {
                any = true;
                // SAFETY: decode checks only whether a projected rule is null;
                // this marker points into a live scratch allocation.
                unsafe { rules.ptr().add(rule).write(rules.ptr().cast()) };
            }
        }
        drop(present);
        if optional && !any {
            return Ok(());
        }
        // SAFETY: the static source stage remains live and the projection keeps
        // original rule positions. The final decoder is zeroed and guarded.
        let status = unsafe {
            bindings::classify_decode(
                context.stages.memory.native_context(),
                c"decode".as_ptr(),
                context.stages.storage.ptr().add(SOURCE),
                rules.ptr(),
                context.stages.count,
                context.destination.add(index).cast(),
            )
        };
        if status != 0 {
            return Err(BuildError::Refused(Refusal::OutOfMemory).into());
        }
        Ok(())
    }
}

pub fn rule_table<E, F>(present: F) -> DecodeInit<F>
where
    F: FnMut(usize) -> Result<bool, E>,
{
    DecodeInit(present, false)
}
pub fn optional_rule_table<E, F>(present: F) -> DecodeInit<F>
where
    F: FnMut(usize) -> Result<bool, E>,
{
    DecodeInit(present, true)
}
pub fn optional_decode<E, F>(present: F) -> DecodeInit<F>
where
    F: FnMut(usize) -> Result<bool, E>,
{
    DecodeInit(present, true)
}
impl<const L: usize, const R: usize, E, F> NodeInit<RuleTable<L, R>, E> for DecodeInit<F>
where
    F: FnMut(usize) -> Result<bool, E>,
{
    unsafe fn compile<W: Owner>(self, index: usize, context: &mut CompileContext<'_, W>) -> Result<(), E>
    where
        E: From<BuildError<W::Error>>,
    {
        let DecodeInit(mut present, optional) = self;
        let rules = Scratch::<*const bindings::classifier_rule>::new(context.stages.count as usize)
            .map_err(BuildError::Refused)?;
        let mut any = false;
        for rule in 0..context.stages.count as usize {
            if present(rule)? {
                any = true;
                // SAFETY: the marker points into live guarded scratch and is
                // used only to distinguish an included original rule slot.
                unsafe { rules.ptr().add(rule).write(rules.ptr().cast()) };
            }
        }
        drop(present);
        if optional && !any {
            return Ok(());
        }
        // SAFETY: topology fixes both preceding stages; all original rule slots
        // remain represented, and partial native output is guarded in place.
        let status = unsafe {
            bindings::classify_join_rules(
                context.stages.memory.native_context(),
                c"rules".as_ptr(),
                context.stages.storage.ptr().add(L),
                context.stages.storage.ptr().add(R),
                rules.ptr(),
                context.stages.count,
                context.destination.add(index).cast(),
            )
        };
        if status != 0 {
            return Err(BuildError::Refused(Refusal::OutOfMemory).into());
        }
        Ok(())
    }
}

macro_rules! borrowed_leaf {
    ($helper:ident,$init:ident,$spec:ty,$item:ty,$view:ty,$compile:ident,$fill:expr,$valid:expr) => {
        pub fn $helper<'src, E, F>(read: F) -> $init<F>
        where
            F: FnMut(usize) -> Result<Option<&'src [$item]>, E>,
        {
            $init(read)
        }
        pub struct $init<F>(F);
        impl<'src, E, F> NodeInit<$spec, E> for $init<F>
        where
            F: FnMut(usize) -> Result<Option<&'src [$item]>, E>,
        {
            unsafe fn compile<W: Owner>(self, index: usize, context: &mut CompileContext<'_, W>) -> Result<(), E>
            where
                E: From<BuildError<W::Error>>,
            {
                let $init(mut read) = self;
                let count = context.stages.count as usize;
                let views = Scratch::<$view>::new(count).map_err(BuildError::Refused)?;
                let rules = Scratch::<*const bindings::classifier_rule>::new(count).map_err(BuildError::Refused)?;
                for rule in 0..count {
                    let Some(items) = read(rule)? else {
                        continue;
                    };
                    if !items.iter().all($valid) {
                        return Err(BuildError::Refused(Refusal::InvalidClassifyInput).into());
                    }
                    let len =
                        u32::try_from(items.len()).map_err(|_| BuildError::Refused(Refusal::InvalidClassifyInput))?;
                    // SAFETY: fixed descriptors borrow validated native-layout
                    // slices until C finishes; null slots preserve exclusions.
                    unsafe {
                        let view = views.ptr().add(rule);
                        ($fill)(view, items, len);
                        rules.ptr().add(rule).write(view.cast());
                    }
                }
                drop(read);
                // SAFETY: native getters only copy these immutable descriptors;
                // final output and failed current stage retain their guards.
                let status = unsafe {
                    bindings::$compile(
                        context.stages.memory.native_context(),
                        rules.ptr(),
                        context.stages.count,
                        context.destination.add(index).cast(),
                        context.stages.storage.ptr().add(index),
                    )
                };
                if status != 0 {
                    return Err(BuildError::Refused(Refusal::OutOfMemory).into());
                }
                Ok(())
            }
        }
    };
}
borrowed_leaf!(
    net6,
    Net6Init,
    Net6,
    Network6,
    bindings::yanet_sys_net6_input,
    yanet_sys_classify_net6_compile,
    |v: *mut bindings::yanet_sys_net6_input, s: &[Network6], n| ptr::addr_of_mut!((*v).nets).write(
        bindings::filter_net6s {
            items: s.as_ptr().cast_mut().cast(),
            count: n
        }
    ),
    valid_net6
);
borrowed_leaf!(
    byte,
    ByteInit,
    ByteLine,
    LineRange,
    bindings::yanet_sys_byte_input,
    yanet_sys_classify_byte_compile,
    |v: *mut bindings::yanet_sys_byte_input, s: &[LineRange], n| ptr::addr_of_mut!((*v).ranges)
        .write(bindings::classify_line_ranges { items: s.as_ptr().cast(), count: n }),
    |r: &LineRange| r.from <= r.to && r.to <= 255
);
borrowed_leaf!(
    port,
    PortInit,
    Port,
    PortRange,
    bindings::yanet_sys_port_input,
    yanet_sys_classify_port_compile,
    |v: *mut bindings::yanet_sys_port_input, s: &[PortRange], n| ptr::addr_of_mut!((*v).ranges)
        .write(bindings::filter_u16_ranges { items: s.as_ptr().cast(), count: n }),
    |r: &PortRange| r.from <= r.to
);
pub fn device<E, F>(read: F) -> DeviceInit<F>
where
    F: FnMut(usize) -> Result<Option<Range<usize>>, E>,
{
    DeviceInit(read)
}
pub struct DeviceInit<F>(F);
impl<E, F> NodeInit<Device, E> for DeviceInit<F>
where
    F: FnMut(usize) -> Result<Option<Range<usize>>, E>,
{
    unsafe fn compile<W: Owner>(self, index: usize, context: &mut CompileContext<'_, W>) -> Result<(), E>
    where
        E: From<BuildError<W::Error>>,
    {
        let DeviceInit(mut read) = self;
        let count = context.stages.count as usize;
        let views = Scratch::<bindings::yanet_sys_device_input>::new(count).map_err(BuildError::Refused)?;
        let rules = Scratch::<*const bindings::classifier_rule>::new(count).map_err(BuildError::Refused)?;
        for rule in 0..count {
            let Some(range) = read(rule)? else {
                continue;
            };
            let (start, len) = device_range(range, context.device_count).map_err(BuildError::Refused)?;
            // SAFETY: bounds were checked before deriving a pointer.
            //
            // Descriptors borrow the name scratch until compilation finishes.
            unsafe {
                let view = views.ptr().add(rule);
                ptr::addr_of_mut!((*view).devices).write(bindings::filter_devices {
                    items: if len == 0 {
                        ptr::null_mut()
                    } else {
                        context
                            .devices
                            .as_ref()
                            .expect("validated nonempty device span")
                            .ptr()
                            .add(start)
                    },
                    count: len,
                });
                rules.ptr().add(rule).write(view.cast());
            }
        }
        drop(read);
        // SAFETY: static C getters only copy complete immutable descriptors;
        // partial native output and the failed current stage remain guarded.
        let status = unsafe {
            bindings::yanet_sys_classify_device_compile(
                context.stages.memory.native_context(),
                rules.ptr(),
                context.stages.count,
                context.destination.add(index).cast(),
                context.stages.storage.ptr().add(index),
            )
        };
        if status != 0 {
            return Err(BuildError::Refused(Refusal::OutOfMemory).into());
        }
        Ok(())
    }
}

pub fn fragment<E, F>(read: F) -> FragmentInit<F>
where
    F: FnMut(usize) -> Result<Option<Fragment>, E>,
{
    FragmentInit(read)
}
pub struct FragmentInit<F>(F);
impl<E, F> NodeInit<IpFrag, E> for FragmentInit<F>
where
    F: FnMut(usize) -> Result<Option<Fragment>, E>,
{
    unsafe fn compile<W: Owner>(self, index: usize, context: &mut CompileContext<'_, W>) -> Result<(), E>
    where
        E: From<BuildError<W::Error>>,
    {
        let FragmentInit(mut read) = self;
        let views = Scratch::<bindings::yanet_sys_fragment_input>::new(context.stages.count as usize)
            .map_err(BuildError::Refused)?;
        let rules = Scratch::<*const bindings::classifier_rule>::new(context.stages.count as usize)
            .map_err(BuildError::Refused)?;
        for rule in 0..context.stages.count as usize {
            if let Some(fragment) = read(rule)? {
                // SAFETY: each initialized scalar descriptor occupies its own
                // slot.
                unsafe {
                    let view = views.ptr().add(rule);
                    ptr::addr_of_mut!((*view).fragment).write(match fragment {
                        Fragment::Any => 0,
                        Fragment::Whole => 1,
                        Fragment::Later => 2,
                    });
                    rules.ptr().add(rule).write(view.cast());
                }
            }
        }
        drop(read);
        // SAFETY: all descriptor callbacks completed under armed guards.
        let status = unsafe {
            bindings::yanet_sys_classify_fragment_compile(
                context.stages.memory.native_context(),
                rules.ptr(),
                context.stages.count,
                context.destination.add(index).cast(),
                context.stages.storage.ptr().add(index),
            )
        };
        if status != 0 {
            return Err(BuildError::Refused(Refusal::OutOfMemory).into());
        }
        Ok(())
    }
}

macro_rules! tuple {
    ($($index:tt:$node:ident:$input:ident:$value:ident),+) => {
        impl<Error, $($node: Spec + 'static, $input: NodeInit<$node, Error>),+> sealed::Init<($($node,)+), Error> for ($($input,)+) {}
        // SAFETY: every position has the matching private native initializer.
        unsafe impl<Error, $($node: Spec + 'static, $input: NodeInit<$node, Error>),+> Initializers<($($node,)+), Error> for ($($input,)+) {
            unsafe fn compile<WriterOwner: Owner>(self, context: &mut CompileContext<'_, WriterOwner>) -> Result<(), Error>
            where Error: From<BuildError<WriterOwner::Error>> {
                let ($($value,)+) = self;
                $(
                    context.stages.visited = $index + 1;
                    // SAFETY: the tuple position matches its validated static node.
                    unsafe { $value.compile($index, context)? };
                )+
                Ok(())
            }
        }
    };
}
macro_rules! tuples {
    (($($old_index:tt:$old_node:ident:$old_input:ident:$old_value:ident),*); $index:tt:$node:ident:$input:ident:$value:ident $(,$next:tt:$rest:ident:$rest_input:ident:$rest_value:ident)*) => {
        tuple!($($old_index:$old_node:$old_input:$old_value,)*$index:$node:$input:$value);
        tuples!(($($old_index:$old_node:$old_input:$old_value,)*$index:$node:$input:$value); $($next:$rest:$rest_input:$rest_value),*);
    };
    (($($old_index:tt:$old_node:ident:$old_input:ident:$old_value:ident),*);) => {};
}
tuples!(() ; 0:N0:I0:n0,1:N1:I1:n1,2:N2:I2:n2,3:N3:I3:n3,4:N4:I4:n4,5:N5:I5:n5,6:N6:I6:n6,7:N7:I7:n7,8:N8:I8:n8,9:N9:I9:n9,10:N10:I10:n10,11:N11:I11:n11,12:N12:I12:n12,13:N13:I13:n13,14:N14:I14:n14,15:N15:I15:n15,16:N16:I16:n16,17:N17:I17:n17,18:N18:I18:n18,19:N19:I19:n19,20:N20:I20:n20,21:N21:I21:n21,22:N22:I22:n22,23:N23:I23:n23,24:N24:I24:n24,25:N25:I25:n25,26:N26:I26:n26,27:N27:I27:n27,28:N28:I28:n28,29:N29:I29:n29,30:N30:I30:n30,31:N31:I31:n31,32:N32:I32:n32,33:N33:I33:n33,34:N34:I34:n34,35:N35:I35:n35,36:N36:I36:n36,37:N37:I37:n37,38:N38:I38:n38,39:N39:I39:n39,40:N40:I40:n40,41:N41:I41:n41,42:N42:I42:n42,43:N43:I43:n43,44:N44:I44:n44,45:N45:I45:n45,46:N46:I46:n46,47:N47:I47:n47,48:N48:I48:n48,49:N49:I49:n49,50:N50:I50:n50,51:N51:I51:n51,52:N52:I52:n52,53:N53:I53:n53,54:N54:I54:n54,55:N55:I55:n55,56:N56:I56:n56,57:N57:I57:n57,58:N58:I58:n58,59:N59:I59:n59,60:N60:I60:n60,61:N61:I61:n61,62:N62:I62:n62,63:N63:I63:n63);

fn device_range(range: Range<usize>, count: usize) -> Result<(usize, u32), Refusal> {
    if range.start > range.end || range.end > count {
        return Err(Refusal::InvalidClassifyInput);
    }
    Ok((
        range.start,
        u32::try_from(range.len()).map_err(|_| Refusal::InvalidClassifyInput)?,
    ))
}
#[cfg(test)]
mod tests {
    use super::{Refusal, device_range};
    #[test]
    fn test_device_range_checks_endpoints_before_deriving_native_pointers() {
        assert_eq!(Ok((1, 2)), device_range(1..3, 3));
        assert_eq!(Ok((3, 0)), device_range(3..3, 3));
        assert_eq!(
            Err(Refusal::InvalidClassifyInput),
            device_range(core::ops::Range { start: 2, end: 1 }, 3)
        );
        assert_eq!(Err(Refusal::InvalidClassifyInput), device_range(0..4, 3));
        assert_eq!(
            Err(Refusal::InvalidClassifyInput),
            device_range(usize::MAX..usize::MAX, 3)
        );
        if usize::BITS > 32 {
            assert_eq!(
                Err(Refusal::InvalidClassifyInput),
                device_range(0..u32::MAX as usize + 1, usize::MAX)
            );
        }
    }
}
