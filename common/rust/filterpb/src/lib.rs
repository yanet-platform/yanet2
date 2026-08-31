use serde::{Deserialize, Deserializer};

#[allow(clippy::all, clippy::std_instead_of_core, non_snake_case)]
pub mod pb {
    tonic::include_proto!("common.filterpb.v1");
}

pub mod network;

/// Deserializes a null as the field's zero value, as the operator's YAML
/// decoder reads it.
pub fn null_as_default<'de, T, D>(deserializer: D) -> Result<T, D::Error>
where
    T: Default + Deserialize<'de>,
    D: Deserializer<'de>,
{
    Ok(Option::<T>::deserialize(deserializer)?.unwrap_or_default())
}
