//! Borrowed rule projection and authored firewall construction.

#![forbid(unsafe_code)]

use core::slice::from_ref;

use yanet_sdk::{
    Built, FfiRecord,
    classify::{init, records::Net4},
    counter_pair, slice,
};
use yanet_sdk_api::{Agent, ApiError, ErrorKind};
use yanet_sdk_fixture::firewall4::{ConfigInit, Firewall4, TargetInit};

#[derive(FfiRecord)]
#[repr(C)]
pub struct Rule4 {
    pub source: yanet_sdk::__private::net4,
    pub destination: yanet_sdk::__private::net4,
    pub actions: [u8; 8],
    pub action_count: u64,
    pub active: u64,
    pub counter: [u8; 64],
}

pub struct Firewall4Api;

#[yanet_sdk::api(module = Firewall4)]
impl Firewall4Api {
    pub fn build(agent: &Agent, name: &str, rules: &[Rule4], fault: u64) -> Result<Built<Firewall4, Agent>, ApiError> {
        let source = Reader { rules, destination: false, fault: 0 };
        let destination = Reader { rules, destination: true, fault };
        agent.build_config(
            name,
            ConfigInit {
                ipv4: init::classify(
                    rules.len(),
                    (
                        init::net4(move |index| source.read(index)),
                        init::net4(move |index| destination.read(index)),
                        init::joint(),
                        init::decode(|index| {
                            if fault == 4 && index == 1 {
                                return Err(ApiError::new(ErrorKind::InvalidArgument, "decode projection fault"));
                            }
                            Ok(rules[index].active != 0)
                        }),
                    ),
                ),
                targets: slice(rules.len(), |index| {
                    let rule = &rules[index];
                    if rule.action_count > 8
                        || rule
                            .actions
                            .iter()
                            .take(rule.action_count as usize)
                            .any(|action| *action > 2)
                    {
                        return Err(ApiError::new(ErrorKind::InvalidArgument, "an action list is invalid"));
                    }
                    let name = core::ffi::CStr::from_bytes_until_nul(&rule.counter)
                        .ok()
                        .and_then(|name| name.to_str().ok())
                        .ok_or_else(|| ApiError::new(ErrorKind::InvalidArgument, "a counter name is invalid"))?;
                    Ok(TargetInit {
                        counter: counter_pair(
                            "rules",
                            CounterName {
                                value: name,
                                panic: fault == 5 && index == 1,
                            },
                        ),
                        actions: rule.actions,
                        action_count: rule.action_count,
                    })
                }),
            },
        )
    }
}

struct Reader<'a> {
    rules: &'a [Rule4],
    destination: bool,
    fault: u64,
}
impl<'a> Reader<'a> {
    fn read(&self, index: usize) -> Result<Option<&'a [Net4]>, ApiError> {
        if self.fault == 1 && index == 1 {
            return Err(ApiError::new(ErrorKind::InvalidArgument, "network projection fault"));
        }
        if self.fault == 2 && index == 1 {
            panic!("network projection panic");
        }
        let rule = &self.rules[index];
        if rule.active == 0 {
            return Ok(None);
        }
        Ok(Some(from_ref(if self.destination {
            &rule.destination
        } else {
            &rule.source
        })))
    }
}
impl Drop for Reader<'_> {
    fn drop(&mut self) {
        if self.fault == 3 {
            panic!("network getter destructor");
        }
    }
}

struct CounterName<'a> {
    value: &'a str,
    panic: bool,
}
impl AsRef<str> for CounterName<'_> {
    fn as_ref(&self) -> &str {
        self.value
    }
}
impl Drop for CounterName<'_> {
    fn drop(&mut self) {
        if self.panic {
            panic!("counter name destructor");
        }
    }
}
