use leptos::prelude::*;

#[derive(Clone, Copy, PartialEq)]
pub enum SpinnerSize {
    Small,
    Medium,
    Large,
}

impl SpinnerSize {
    #[inline]
    pub const fn as_class(&self) -> &'static str {
        match self {
            Self::Small => "spin-ss",
            Self::Medium => "spin-sm",
            Self::Large => "spin-sl",
        }
    }
}

impl Default for SpinnerSize {
    fn default() -> Self {
        Self::Small
    }
}

#[component]
pub fn Spinner(
    /// Semantic spinner size.
    #[prop(default = SpinnerSize::Small)]
    size: SpinnerSize,
) -> impl IntoView {
    view! {
        <div class=format!("spin {}", size.as_class())>
            <div class="spin__l"></div>
            <div class="spin__c"></div>
            <div class="spin__r"></div>
        </div>
    }
}
