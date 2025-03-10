use std::borrow::Cow;

use leptos::prelude::*;

/// Popup wrapper that displays additional information attached to children
/// components on hover (by default).
///
/// Unlike traditional approach for managing popups mounted directly to the
/// page body this will be attached to the children.
#[component]
pub fn SpanPopup(
    /// Message that is required to be displayed.
    #[prop(into)]
    message: Signal<Cow<'static, str>>,
    /// Nested children.
    children: Children,
) -> impl IntoView {
    let message = move || {
        match message.get() {
            message if message.is_empty() => {
                // Render nothing if there is no message.
                None
            }
            message => {
                // Otherwise render something useful.
                Some(view! { <label class="noc-sp__message">{message}</label> })
            }
        }
    };

    view! { <div class="noc-sp">{children()} {message}</div> }
}
