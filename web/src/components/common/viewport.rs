use leptos::prelude::*;

/// Page viewport component.
///
/// This component is responsible for pre-styled viewport rendering, which is
/// the main component of a page.
#[component]
pub fn Viewport(
    /// Nested children.
    #[prop(optional)]
    children: Option<Children>,
) -> impl IntoView {
    view! { <div class="viewport">{children.map(|c| c())}</div> }
}

/// Viewport's main content component.
#[component]
pub fn ViewportContent(
    /// Nested children.
    #[prop(optional)]
    children: Option<Children>,
) -> impl IntoView {
    view! { <div class="viewport__content">{children.map(|c| c())}</div> }
}
