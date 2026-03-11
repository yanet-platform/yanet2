use core::{error::Error, str::FromStr};

use crate::pb;

impl FromStr for pb::FunctionChain {
    type Err = Box<dyn Error>;

    fn from_str(s: &str) -> Result<Self, Self::Err> {
        let (chain_part, modules_part) = s
            .split_once('=')
            .ok_or_else(|| format!("invalid chain format: expected 'name:weight=modules', got {s:?}"))?;
        let (name, weight) = chain_part
            .split_once(':')
            .ok_or_else(|| format!("invalid chain format: expected 'name:weight', got {chain_part:?}"))?;
        let weight = weight
            .parse::<u64>()
            .map_err(|e| format!("invalid chain weight {weight:?}: {e}"))?;
        let modules = modules_part
            .split(',')
            .map(|m| -> Result<::commonpb::pb::ModuleId, Box<dyn Error>> {
                let (r#type, name) = m
                    .split_once(':')
                    .ok_or_else(|| format!("invalid module format: expected 'module_type:config_name', got {m:?}"))?;
                Ok(::commonpb::pb::ModuleId {
                    r#type: r#type.to_string(),
                    name: name.to_string(),
                })
            })
            .collect::<Result<Vec<_>, _>>()?;
        Ok(pb::FunctionChain {
            weight,
            chain: Some(pb::Chain { name: name.to_string(), modules }),
        })
    }
}
