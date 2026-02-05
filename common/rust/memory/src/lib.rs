/// Get a pointer to the containing struct from a pointer to one of its fields.
///
/// Given a pointer to a field within a struct, this macro computes the pointer
/// to the containing struct instance. This is the Rust equivalent of the C
/// `container_of` macro.
///
/// # Safety
/// The caller must ensure that:
/// - `$ptr` is a valid pointer to a field of type `$field` within a `$type` struct
/// - The resulting pointer is valid for the intended use
///
/// # Arguments
/// * `$ptr` - A pointer to a field within the struct
/// * `$type` - The containing struct type
/// * `$field` - The field name that `$ptr` points to
///
/// # Returns
/// A `*mut $type` pointer to the containing struct
///
/// # Example
/// ```ignore
/// struct MyStruct {
///     a: u32,
///     b: u64,
/// }
///
/// // Given a pointer to the `b` field:
/// let b_ptr: *mut u64 = /* ... */;
/// let struct_ptr: *mut MyStruct = container_of!(b_ptr, MyStruct, b);
/// ```
#[macro_export]
macro_rules! container_of {
    ($ptr:expr, $type:ty, $field:ident) => {{
        let ptr = $ptr as *const u8;
        let offset = $crate::offset_of!($type, $field);
        unsafe { (ptr.sub(offset)) as *mut $type }
    }};
}

/// Convert a relative pointer (offset) to an absolute virtual address.
///
/// A relative pointer P points to the virtual address (&P + P).
/// If P is NULL (0), it returns NULL.
///
/// This macro takes a raw pointer to the offset field and returns
/// the absolute address it points to.
///
/// # Safety
/// The caller must ensure that the offset pointer is valid and that
/// the resulting address is valid for the intended use.
///
/// # Example
/// ```ignore
/// // For a field that stores a relative offset:
/// let agent_ptr: *mut agent = addr_of!(core::ptr::addr_of_mut!((*module).agent));
/// ```
#[macro_export]
macro_rules! addr_of {
    ($offset_ptr:expr) => {{
        let offset_ptr = $offset_ptr as *const usize;
        let offset_val = unsafe { *offset_ptr };
        if offset_val == 0 {
            core::ptr::null_mut()
        } else {
            (offset_val.wrapping_add(offset_ptr as usize)) as *mut _
        }
    }};
}

/// Set a relative pointer to point to a virtual address.
///
/// This macro sets a relative pointer to point to a specified virtual address.
/// After this macro is called, it is guaranteed that addr_of!(ptr) == addr.
///
/// # Safety
/// The caller must ensure that both pointers are valid.
///
/// # Example
/// ```ignore
/// // Set a relative offset field to point to an address:
/// set_offset_of!(core::ptr::addr_of_mut!((*module).agent), agent_ptr);
/// ```
#[macro_export]
macro_rules! set_offset_of {
    ($ptr:expr, $addr:expr) => {{
        let ptr = $ptr as *mut usize;
        let addr = $addr as usize;
        unsafe {
            if addr == 0 {
                *ptr = 0;
            } else {
                *ptr = addr.wrapping_sub(ptr as usize);
            }
        }
    }};
}

/// Calculate the byte offset of a field within a struct.
///
/// This macro computes the offset in bytes from the start of a struct
/// to a specified field, similar to the C `offsetof` macro.
///
/// # Arguments
/// * `$type` - The struct type
/// * `$field` - The field name within the struct
///
/// # Returns
/// The offset in bytes as a `usize`
///
/// # Example
/// ```ignore
/// struct MyStruct {
///     a: u32,
///     b: u64,
/// }
///
/// let offset = offset_of!(MyStruct, b);
/// // offset is the byte offset of field `b` within `MyStruct`
/// ```
#[macro_export]
macro_rules! offset_of {
    ($type:ty, $field:ident) => {{
        let uninit = core::mem::MaybeUninit::<$type>::uninit();
        let base_ptr = uninit.as_ptr();
        let field_ptr = unsafe { core::ptr::addr_of!((*base_ptr).$field) };
        (field_ptr as usize) - (base_ptr as usize)
    }};
}

/// Assign one relative pointer to another.
///
/// This macro makes an assignment in the sense of relative pointers,
/// so that addr_of!(dst) == addr_of!(src) after the call.
///
/// # Safety
/// The caller must ensure that both pointers are valid.
#[macro_export]
macro_rules! equate_offset {
    ($dst:expr, $src:expr) => {{
        $crate::set_offset_of!($dst, $crate::addr_of!($src))
    }};
}