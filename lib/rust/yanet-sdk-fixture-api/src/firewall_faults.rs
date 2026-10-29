//! Late-failure export used only by rollback fixtures.
#![forbid(unsafe_code)]
use yanet_sdk::{
    Built,
    classify::records::{LineRange, Net4, Net6, PortRange},
};
use yanet_sdk_api::{Agent, ApiError, ErrorKind};
use yanet_sdk_fixture::firewall::Firewall as FirewallModule;

use crate::firewall::{DeviceName, Rule};
pub struct FirewallFaults;
#[yanet_sdk::api(module=FirewallModule)]
impl FirewallFaults {
    #[allow(clippy::too_many_arguments)]
    pub fn build(
        agent: &Agent,
        name: &str,
        rules: &[Rule],
        devices: &[DeviceName],
        nets4: &[Net4],
        nets6: &[Net6],
        ports: &[PortRange],
        lines: &[LineRange],
    ) -> Result<Built<FirewallModule, Agent>, ApiError> {
        crate::firewall::build_fault(agent, name, rules, devices, nets4, nets6, ports, lines)
    }
    #[allow(clippy::too_many_arguments)]
    pub fn names_drop(
        agent: &Agent,
        name: &str,
        rules: &[Rule],
        devices: &[DeviceName],
        nets4: &[Net4],
        nets6: &[Net6],
        ports: &[PortRange],
        lines: &[LineRange],
    ) -> Result<Built<FirewallModule, Agent>, ApiError> {
        let reader = Names { devices };
        agent.build_config(
            name,
            crate::firewall::input(
                rules,
                devices,
                nets4,
                nets6,
                ports,
                lines,
                move |index| reader.read(index),
                false,
            ),
        )
    }
}

struct Names<'a> {
    devices: &'a [DeviceName],
}
impl<'a> Names<'a> {
    fn read(&self, index: usize) -> Result<&'a str, ApiError> {
        core::ffi::CStr::from_bytes_until_nul(&self.devices[index].name)
            .ok()
            .and_then(|s| s.to_str().ok())
            .ok_or_else(|| ApiError::new(ErrorKind::InvalidArgument, "a device name is invalid"))
    }
}
impl Drop for Names<'_> {
    fn drop(&mut self) {
        panic!("device names callback destructor");
    }
}
