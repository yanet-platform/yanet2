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
    /// Optional class.
    #[prop(optional, into)]
    class: Option<&'static str>,
    /// Nested children.
    #[prop(optional)]
    children: Option<Children>,
) -> impl IntoView {
    let mut cls = "viewport__content".to_string();
    if let Some(c) = class {
        cls.push(' ');
        cls.push_str(c);
    }

    view! { <div class=cls>{children.map(|c| c())}</div> }
}
