//! Early validation of names before preparing a build's inputs.

use yanet_sys::{
    bindings::{CP_MODULE_NAME_LEN, CP_OBJECT_NAME_LEN},
    cp::names,
};

use crate::{Error, ErrorKind};

/// Checks an obligatory name and identifies its input in the error.
///
/// The capacity includes the terminating NUL; length is measured in bytes.
pub fn validate_name(what: &str, name: &str, capacity: usize) -> Result<(), Error> {
    names::validate_name(name, capacity)
        .map_err(|err| Error::new(ErrorKind::InvalidArgument, &format!("{what}: {err}")))
}

/// Checks a module configuration name against its C field's capacity.
pub fn validate_module_name(what: &str, name: &str) -> Result<(), Error> {
    validate_name(what, name, CP_MODULE_NAME_LEN as usize)
}

/// Checks an object name against its C field's capacity.
pub fn validate_object_name(what: &str, name: &str) -> Result<(), Error> {
    validate_name(what, name, CP_OBJECT_NAME_LEN as usize)
}
