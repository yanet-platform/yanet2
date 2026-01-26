#[macro_export]
macro_rules! offset_of {
    ($type:ty, $field:ident) => {{
        let uninit = core::mem::MaybeUninit::<$type>::uninit();
        let base_ptr = uninit.as_ptr();
        let field_ptr = unsafe { core::ptr::addr_of!((*base_ptr).$field) };
        (field_ptr as usize) - (base_ptr as usize)
    }};
}

#[macro_export]
macro_rules! container_of {
    ($ptr:expr, $type:ty, $field:ident) => {{
        let ptr = $ptr as *const u8;
        let offset = $crate::offset_of!($type, $field);
        unsafe { (ptr.sub(offset)) as *mut $type }
    }};
}