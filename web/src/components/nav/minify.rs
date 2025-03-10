use leptos::prelude::*;

use crate::components::common::icons::{Icon, IconKind};

#[component]
pub fn MinifyButton(
    /// Title of the button.
    #[prop(optional, into)]
    title: MaybeProp<&'static str>,
) -> impl IntoView {
    view! {
        <button class="minify-button" title=move || title.get()>
            <div class="icon">
                <Icon kind=IconKind::Triangle />
            </div>
        </button>
    }
}
