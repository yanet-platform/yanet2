use std::mem::MaybeUninit;

use bindings::cp_module;
use crate::state::State;

#[repr(C)]
pub struct Module {
    pub cp_module: MaybeUninit<cp_module>,

    pub state: *mut State,
}
