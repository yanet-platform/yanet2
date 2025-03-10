//! Progress bar.

use leptos::prelude::*;

#[component]
pub fn ProgressBar(
    /// Decimal number between 0.0 and 1.0, which specifies how much of the task
    /// has been completed.
    ///
    /// This value is clamped to the range [0.0, 1.0] before being used.
    #[prop(into, optional)]
    value: Signal<f64>,
    /// ProgressBar color.
    #[prop(into, optional)]
    color: Signal<ProgressBarColor>,
) -> impl IntoView {
    // Clamp the value to the range [0.0, 1.0].
    let value = move || value.get().clamp(0.0, 1.0);

    let class = move || format!("noc-progressbar {}", color.get().as_class());
    let style = move || format!("width: {:.02}%;", value() / 1.0 * 100.0);

    view! {
        <div class=class role="progressbar" aria_valuemax="1" aria-valuemin="0" aria-valuenow=value>
            <div class="progressbar__bar" style=style></div>
        </div>
    }
}

/// [`ProgressBar`] color.
#[derive(Default, Clone)]
pub enum ProgressBarColor {
    #[default]
    Brand,
    Error,
    Warning,
    Success,
}

impl ProgressBarColor {
    /// Returns the CSS class for this color.
    pub const fn as_class(&self) -> &'static str {
        match self {
            Self::Brand => "brand",
            Self::Error => "error",
            Self::Warning => "warning",
            Self::Success => "success",
        }
    }
}
