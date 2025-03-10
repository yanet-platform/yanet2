//! UI settings.

use std::str::FromStr;

use leptos::prelude::{signal, window, Get, ReadSignal, Update, WriteSignal};

const IS_MINIFIED_KEY: &str = "ui.settings.sidebar.is_minified";

/// Sidebar settings context.
#[derive(Clone, Copy)]
pub struct SideBarContext {
    is_compact: ReadSignal<bool>,
    set_is_compact: WriteSignal<bool>,
}

impl SideBarContext {
    /// Constructs a new sidebar context.
    pub fn new() -> Self {
        let mut value = Default::default();
        if let Ok(Some(storage)) = window().local_storage() {
            if let Ok(Some(v)) = storage.get_item(IS_MINIFIED_KEY) {
                value = bool::from_str(&v).unwrap_or_default();
            }
        }

        let (is_compact, set_is_compact) = signal(value);

        Self { is_compact, set_is_compact }
    }

    /// Returns whether the sidebar should be minified.
    pub fn is_compact(&self) -> bool {
        self.is_compact.get()
    }

    /// Sets the current minified value for the sidebar in the local storage,
    /// notifying subscribers.
    pub fn set_compact(&self, is_compact: bool) {
        if let Ok(Some(storage)) = window().local_storage() {
            let v = format!("{is_compact}");
            if storage.set_item(IS_MINIFIED_KEY, &v).is_err() {
                log::warn!("failed to update the local storage");
            }

            self.set_is_compact.update(|v| {
                *v = is_compact;
            })
        }
    }

    /// Switches the current minified value for the sidebar in the local
    /// storage, notifying subscribers.
    pub fn switch(&self) {
        self.set_compact(!self.is_compact.get());
    }
}
