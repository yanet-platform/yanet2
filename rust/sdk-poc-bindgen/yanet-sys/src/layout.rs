//! Classification of the C fields the SDK projects into.
//!
//! The crate-private `layout!` declaration names, for every C
//! aggregate the SDK reads, how each field may be used: a relative pointer
//! (`rel`), an absolute address into the shared-memory mapping (`abs`), a
//! foreign pointer whose own provenance is used as loaded (`ffi`), plain data
//! (`plain`), an embedded aggregate with its own declaration (`embed`), or
//! bytes Rust never reads (`opaque`). bindgen cannot tell a relative from an
//! absolute pointer: both are `T *` in C. The build therefore fails when a
//! declared aggregate leaves any pointer-carrying field unclassified.

/// One field of a bindgen aggregate, as the build script saw it.
pub struct CField {
    pub aggregate: &'static str,
    pub field: &'static str,
    /// The field's bytes hold a pointer, directly or inside a nested
    /// aggregate.
    pub carries_pointer: bool,
    /// The field itself is a data or function pointer (or an array of them).
    pub is_pointer: bool,
}

/// How a declared field may be used.
#[allow(non_camel_case_types)]
#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub enum Class {
    rel,
    abs,
    ffi,
    plain,
    embed,
    opaque,
}

/// One classified field of a `layout!` declaration.
pub struct Decl {
    pub aggregate: &'static str,
    pub field: &'static str,
    pub class: Class,
}

const fn str_eq(a: &str, b: &str) -> bool {
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

/// Reports whether a declaration table names the aggregate.
pub const fn declares(decl: &[Decl], aggregate: &str) -> bool {
    let mut idx = 0;
    while idx < decl.len() {
        if str_eq(decl[idx].aggregate, aggregate) {
            return true;
        }
        idx += 1;
    }
    false
}

/// Reports whether a declaration table classifies the field.
pub const fn classifies(decl: &[Decl], aggregate: &str, field: &str) -> bool {
    let mut idx = 0;
    while idx < decl.len() {
        if str_eq(decl[idx].aggregate, aggregate) && str_eq(decl[idx].field, field) {
            return true;
        }
        idx += 1;
    }
    false
}

const fn bindgen_field(aggregate: &str, field: &str) -> Option<&'static CField> {
    let fields = crate::bindgen_fields::BINDGEN_FIELDS;
    let mut idx = 0;
    while idx < fields.len() {
        if str_eq(fields[idx].aggregate, aggregate) && str_eq(fields[idx].field, field) {
            return Some(&fields[idx]);
        }
        idx += 1;
    }
    None
}

/// Reports whether bindgen saw the field as a pointer.
pub const fn field_is_pointer(aggregate: &str, field: &str) -> bool {
    match bindgen_field(aggregate, field) {
        Some(field) => field.is_pointer,
        None => false,
    }
}

/// Reports whether the field's bytes hold a pointer anywhere.
pub const fn field_carries_pointer(aggregate: &str, field: &str) -> bool {
    match bindgen_field(aggregate, field) {
        Some(field) => field.carries_pointer,
        None => true,
    }
}

/// Size of the type a raw pointer points to.
pub const fn size_of_pointee<T>(_: *const T) -> usize {
    size_of::<T>()
}

/// Types every bit pattern of which is a valid value.
///
/// # Safety
///
/// Implementors must have no invalid bit patterns and no padding.
pub unsafe trait AnyBitPattern {}

// SAFETY: primitive integers accept every bit pattern.
unsafe impl AnyBitPattern for u8 {}
// SAFETY: as above.
unsafe impl AnyBitPattern for u16 {}
// SAFETY: as above.
unsafe impl AnyBitPattern for u32 {}
// SAFETY: as above.
unsafe impl AnyBitPattern for u64 {}
// SAFETY: as above.
unsafe impl AnyBitPattern for usize {}
// SAFETY: an array of such elements has no padding and no invalid pattern.
unsafe impl<T: AnyBitPattern, const N: usize> AnyBitPattern for [T; N] {}

/// Compile-time witness that a type accepts every bit pattern.
pub const fn assert_any_bit_pattern<T: AnyBitPattern>() {}

/// Declares how the SDK projects into C aggregates and generates the views.
///
/// Each entry is `MODE Name = c_aggregate { CLASS field[: Type], ... }`:
///
/// - `frozen`: a view over memory frozen for the generation `'g`. Rust never
///   forms a reference to the whole aggregate, only to the declared non-opaque
///   fields, so C may keep mutating opaque bytes (a registry refcount, the
///   sibling link of an embedded memory context).
/// - `mirror` / `mirror_union`: a `repr(C)` Rust type whose layout is asserted
///   against bindgen field by field; references to the whole aggregate are
///   allowed, so every field must be frozen.
/// - `raw`: field-address functions for memory owned by the current invocation
///   (packets, fronts, mbufs).
///
/// Crate-private on purpose: the generated accessors dereference the view's
/// pointer and mirror types can be built from their fields, so a declaration
/// is a statement about C memory that only this crate may make.
macro_rules! layout {
    ($(
        $(#[$meta:meta])*
        $mode:ident $name:ident = $c:ident {
            $( $class:ident $field:ident $(: $ty:ty)? ),* $(,)?
        }
    )*) => {
        #[doc(hidden)]
        pub const __LAYOUT_DECL: &[$crate::layout::Decl] = &[
            $( $( $crate::layout::Decl {
                aggregate: ::core::stringify!($c),
                field: ::core::stringify!($field),
                class: $crate::layout::Class::$class,
            }, )* )*
        ];
        const _: () = $crate::bindgen_fields::check_unclassified(__LAYOUT_DECL);
        $( $( $crate::layout::layout_check!($class $c $field $(: $ty)?); )* )*
        $( $crate::layout::layout_item!($(#[$meta])* $mode $name = $c { $( $class $field $(: $ty)? ),* }); )*
    };
}
pub(crate) use layout;

/// Reports whether a field may carry the given class.
///
/// Pointer classes need a pointer field, `plain` a field without any
/// pointer inside, `embed` a non-pointer aggregate; `opaque` fits anything.
pub const fn class_fits(aggregate: &str, field: &str, class: Class) -> bool {
    match class {
        Class::rel | Class::abs | Class::ffi => field_is_pointer(aggregate, field),
        Class::plain => !field_carries_pointer(aggregate, field),
        Class::embed => !field_is_pointer(aggregate, field),
        Class::opaque => true,
    }
}

/// Per-field compile-time checks of a `layout!` declaration.
macro_rules! layout_check {
    (opaque $c:ident $field:ident) => {
        const _: usize = ::core::mem::offset_of!($crate::bindings::$c, $field);
    };
    ($class:ident $c:ident $field:ident : $ty:ty) => {
        const _: () = ::core::assert!(
            $crate::layout::class_fits(
                ::core::stringify!($c),
                ::core::stringify!($field),
                $crate::layout::Class::$class,
            ),
            ::core::concat!(
                "layout!: `",
                ::core::stringify!($c),
                ".",
                ::core::stringify!($field),
                "` cannot be declared ",
                ::core::stringify!($class)
            ),
        );
        $crate::layout::layout_type_check!($class $c $field $ty);
    };
}
pub(crate) use layout_check;

/// Type checks of one declared field against the bindgen field.
macro_rules! layout_type_check {
    (plain $c:ident $field:ident $ty:ty) => {
        // Plain data is declared with exactly the bindgen field type.
        #[allow(unused_unsafe)]
        const _: fn($crate::bindings::$c) -> $ty = |value| unsafe { value.$field };
    };
    (rel $c:ident $field:ident $ty:ty) => {
        $crate::layout::layout_size_check!($c $field $ty);
        const _: () = $crate::rel::assert_rel_slot::<$ty>();
    };
    (ffi $c:ident $field:ident $ty:ty) => {
        // The declared pointee is exactly the C pointee.
        const _: fn($crate::bindings::$c) -> *mut $ty = |value| value.$field;
    };
    // An absolute address is used for its address only; an embedded
    // aggregate is checked by its own declaration.
    ($class:ident $c:ident $field:ident $ty:ty) => {};
}
pub(crate) use layout_type_check;

macro_rules! layout_size_check {
    ($c:ident $field:ident $ty:ty) => {
        const _: () = {
            let probe = ::core::mem::MaybeUninit::<$crate::bindings::$c>::uninit();
            // SAFETY: only the field address is computed; nothing is read.
            let field = unsafe { &raw const (*probe.as_ptr()).$field };
            ::core::assert!(
                $crate::layout::size_of_pointee(field) == ::core::mem::size_of::<$ty>(),
                ::core::concat!(
                    "layout!: `",
                    ::core::stringify!($c),
                    ".",
                    ::core::stringify!($field),
                    "` has a different size than its declared type"
                ),
            );
        };
    };
}
pub(crate) use layout_size_check;

/// Generates the item of one `layout!` entry.
macro_rules! layout_item {
    ($(#[$meta:meta])* frozen $name:ident = $c:ident { $( $class:ident $field:ident $(: $ty:ty)? ),* }) => {
        $(#[$meta])*
        pub struct $name<'g, R> {
            raw: ::core::ptr::NonNull<$crate::bindings::$c>,
            res: R,
            _graph: ::core::marker::PhantomData<&'g ()>,
        }

        impl<'g, R: $crate::rel::Resolver<'g>> $crate::rel::FrozenView<'g, R> for $name<'g, R> {
            type C = $crate::bindings::$c;

            unsafe fn from_raw(res: R, raw: ::core::ptr::NonNull<Self::C>) -> Self {
                Self { raw, res, _graph: ::core::marker::PhantomData }
            }
        }

        impl<'g, R: $crate::rel::Resolver<'g>> $name<'g, R> {
            /// Resolver the view was reached through.
            pub fn resolver(&self) -> R {
                self.res
            }

            /// Address of the viewed aggregate.
            pub fn addr(&self) -> usize {
                self.raw.as_ptr().addr()
            }

            $( $crate::layout::layout_accessor!($class $field $(: $ty)?); )*
        }
    };
    ($(#[$meta:meta])* mirror $name:ident = $c:ident { $( $class:ident $field:ident : $ty:ty ),* }) => {
        $(#[$meta])*
        #[repr(C)]
        pub struct $name {
            $( $field: $ty, )*
        }

        const _: () = {
            ::core::assert!(::core::mem::size_of::<$name>() == ::core::mem::size_of::<$crate::bindings::$c>());
            ::core::assert!(::core::mem::align_of::<$name>() == ::core::mem::align_of::<$crate::bindings::$c>());
            $( ::core::assert!(
                ::core::mem::offset_of!($name, $field) == ::core::mem::offset_of!($crate::bindings::$c, $field)
            ); )*
        };

        impl $name {
            $(
                pub fn $field(&self) -> &$ty {
                    &self.$field
                }
            )*
        }
    };
    ($(#[$meta:meta])* mirror_union $name:ident = $c:ident { $( $class:ident $field:ident : $ty:ty ),* }) => {
        $(#[$meta])*
        #[repr(C)]
        pub union $name {
            $( $field: ::core::mem::ManuallyDrop<$ty>, )*
        }

        const _: () = {
            ::core::assert!(::core::mem::size_of::<$name>() == ::core::mem::size_of::<$crate::bindings::$c>());
            ::core::assert!(::core::mem::align_of::<$name>() == ::core::mem::align_of::<$crate::bindings::$c>());
            $( ::core::assert!(::core::mem::size_of::<$ty>() == ::core::mem::size_of::<$name>()); )*
            $( $crate::layout::layout_union_variant!($class $ty); )*
        };

        impl $name {
            $(
                pub fn $field(&self) -> &$ty {
                    // SAFETY: every variant spans the whole union and accepts
                    // every bit pattern (checked above).
                    unsafe { &self.$field }
                }
            )*
        }
    };
    ($(#[$meta:meta])* raw $name:ident = $c:ident { $( $class:ident $field:ident $(: $ty:ty)? ),* }) => {
        $(#[$meta])*
        pub struct $name;

        impl $name {
            $( $crate::layout::layout_raw_accessor!($class $c $field $(: $ty)?); )*
        }
    };
}
pub(crate) use layout_item;

macro_rules! layout_union_variant {
    (rel $ty:ty) => {
        $crate::rel::assert_rel_slot::<$ty>()
    };
    (plain $ty:ty) => {
        $crate::layout::assert_any_bit_pattern::<$ty>()
    };
}
pub(crate) use layout_union_variant;

/// Accessor of one field of a frozen view.
macro_rules! layout_accessor {
    (opaque $field:ident) => {};
    (plain $field:ident : $ty:ty) => {
        $crate::layout::layout_accessor!(@ref $field $ty);
    };
    (rel $field:ident : $ty:ty) => {
        $crate::layout::layout_accessor!(@ref $field $ty);
    };
    (@ref $field:ident $ty:ty) => {
        pub fn $field(&self) -> &'g $ty {
            // SAFETY: the view's construction contract makes the aggregate
            // live and frozen for 'g; the reference covers this field only,
            // and the declaration checked its type or size.
            unsafe { &*(&raw const (*self.raw.as_ptr()).$field).cast::<$ty>() }
        }
    };
    (abs $field:ident : $ty:ty) => {
        pub fn $field(&self) -> $crate::rel::AbsAddr<$ty> {
            // SAFETY: as for references; only the stored address is used.
            let ptr = unsafe { (&raw const (*self.raw.as_ptr()).$field).read() };
            $crate::rel::AbsAddr::new(ptr.addr())
        }
    };
    (ffi $field:ident : $ty:ty) => {
        pub fn $field(&self) -> *mut $ty {
            // SAFETY: as for references; the pointer is returned unread.
            unsafe { (&raw const (*self.raw.as_ptr()).$field).read() }
        }
    };
    (embed $field:ident : $ty:ty) => {
        pub fn $field(&self) -> $ty {
            // SAFETY: an embedded aggregate shares the enclosing view's
            // lifetime and freeze contract.
            unsafe {
                let field = &raw const (*self.raw.as_ptr()).$field;
                <$ty as $crate::rel::FrozenView<'g, R>>::from_raw(
                    self.res,
                    ::core::ptr::NonNull::new_unchecked(field.cast_mut()),
                )
            }
        }
    };
}
pub(crate) use layout_accessor;

/// Field-address function of one field of a raw view.
macro_rules! layout_raw_accessor {
    (opaque $c:ident $field:ident) => {};
    (ffi $c:ident $field:ident : $ty:ty) => {
        pub const fn $field(raw: *mut $crate::bindings::$c) -> *mut *mut $ty {
            raw.wrapping_byte_add(::core::mem::offset_of!($crate::bindings::$c, $field))
                .cast()
        }
    };
    ($class:ident $c:ident $field:ident : $ty:ty) => {
        pub const fn $field(raw: *mut $crate::bindings::$c) -> *mut $ty {
            // A raw view names the bindgen field type exactly.
            #[allow(unused_unsafe)]
            const {
                let _: fn($crate::bindings::$c) -> $ty = |value| unsafe { value.$field };
            }
            raw.wrapping_byte_add(::core::mem::offset_of!($crate::bindings::$c, $field))
                .cast()
        }
    };
}
pub(crate) use layout_raw_accessor;
