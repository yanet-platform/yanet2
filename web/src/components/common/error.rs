use leptos::prelude::*;

use crate::components::common::{
    center::Center,
    icons::{Icon, IconKind},
};

/// Component that displays an error message.
#[component]
pub fn ErrorView(
    /// Short error description.
    #[prop(into)]
    title: String,
    /// Detailed error description.
    #[prop(into, default = None)]
    details: Option<String>,
    /// Icon to display.
    #[prop(default = IconKind::ErrorBell)]
    icon: IconKind,
) -> impl IntoView {
    view! {
        <Center>
            <div class="error-container">
                <div class="error-icon">
                    <Icon kind={icon}/>
                </div>
                <div class="error-title">{title}</div>
                {details
                    .map(|details| {
                        view! { <div class="error-details">{details}</div> }
                    })}
            </div>
        </Center>
    }
}
