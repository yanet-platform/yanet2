use core::fmt::{self, Display, Formatter};
use std::{error::Error, str::FromStr};

use leptos::prelude::*;

const THEME_KEY: &str = "ui.theme";

/// An error that is returned during transformations in case of unknown value.
#[derive(Debug, Clone, Copy)]
pub struct UnknownTheme;

impl Display for UnknownTheme {
    fn fmt(&self, fmt: &mut Formatter) -> Result<(), fmt::Error> {
        fmt.write_str("invalid theme")
    }
}

impl Error for UnknownTheme {}

/// UI theme.
#[derive(Clone, Copy)]
pub enum Theme {
    /// Light theme.
    Light,
    /// Dark theme.
    Dark,
}

impl Theme {
    /// Returns theme's string representation.
    #[inline]
    pub const fn as_str(&self) -> &'static str {
        match self {
            Self::Light => "light",
            Self::Dark => "dark",
        }
    }
}

impl Default for Theme {
    fn default() -> Self {
        Self::Light
    }
}

impl FromStr for Theme {
    type Err = UnknownTheme;

    fn from_str(v: &str) -> Result<Self, Self::Err> {
        match v {
            "light" => Ok(Self::Light),
            "dark" => Ok(Self::Dark),
            _ => Err(UnknownTheme),
        }
    }
}

/// UI theme context.
#[derive(Clone, Copy)]
pub struct ThemeContext {
    theme: ReadSignal<Theme>,
    set_theme: WriteSignal<Theme>,
}

impl ThemeContext {
    /// Constructs a new theme context.
    ///
    /// This function takes the theme initial value from the local storage, if
    /// possible, and updates body's root class to apply styles globally.
    pub fn new() -> Self {
        let mut value = Default::default();
        if let Ok(Some(storage)) = window().local_storage() {
            if let Ok(Some(v)) = storage.get_item(THEME_KEY) {
                value = Theme::from_str(&v).unwrap_or_default();
            }
        }

        let (theme, set_theme) = signal(value);

        let m = Self { theme, set_theme };
        m.update_body(value);
        m
    }

    /// Returns currently set theme.
    ///
    /// # Note
    ///
    /// This method returns the currently memoized value, not the stored one
    /// in the local storage.
    pub fn current(&self) -> Theme {
        self.theme.get()
    }

    /// Updates the current theme in the local storage, notifying subscribers.
    pub fn set(&self, theme: Theme) {
        if let Ok(Some(storage)) = window().local_storage() {
            if storage.set_item(THEME_KEY, theme.as_str()).is_err() {
                log::warn!("failed to update theme in the local storage");
            }

            self.update_body(theme);
            self.set_theme.update(|v| {
                *v = theme;
            })
        }
    }

    /// Updates <body> class name to match the current theme.
    fn update_body(&self, theme: Theme) {
        document()
            .body()
            .expect("body must exist")
            .set_class_name(&format!("noc-theme_{}", theme.as_str()));
    }
}
