use core::{error::Error, str::FromStr};

#[allow(clippy::all, non_snake_case)]
pub mod pb {
    tonic::include_proto!("commonpb");
}

impl FromStr for pb::DevicePipeline {
    type Err = Box<dyn Error>;

    fn from_str(s: &str) -> Result<Self, Self::Err> {
        let (name, weight) = s
            .split_once(':')
            .ok_or_else(|| format!("invalid pipeline format '{s}': expected 'name:weight'"))?;
        let weight = weight
            .parse::<u64>()
            .map_err(|e| format!("invalid weight in '{s}': {e}"))?;
        Ok(pb::DevicePipeline { name: name.to_string(), weight })
    }
}
