//! The scopes and reports the crate's tests build their cases from.

use readinesspb::pb::{Reason, Scope, State};

use crate::ServiceReport;

/// A reason-less scope in `state`.
pub fn scope(name: &str, state: State) -> Scope {
    Scope {
        name: name.to_owned(),
        state: state as i32,
        reasons: Vec::new(),
        observed_at: None,
        last_transition_time: None,
        expected_observation_interval: None,
    }
}

/// A scope in `state` carrying one message-less reason with `code`.
pub fn scope_with_reason(name: &str, state: State, code: &str) -> Scope {
    Scope {
        reasons: vec![Reason {
            code: code.to_owned(),
            message: String::new(),
        }],
        ..scope(name, state)
    }
}

/// A probed service's report with `scopes`.
pub fn report(service: &str, scopes: Vec<Scope>) -> ServiceReport {
    ServiceReport {
        service: service.to_owned(),
        scopes,
        error: None,
    }
}

/// The report of a service whose probe failed.
pub fn failed_report(service: &str) -> ServiceReport {
    ServiceReport {
        service: service.to_owned(),
        scopes: Vec::new(),
        error: Some("unknown service".to_owned()),
    }
}
