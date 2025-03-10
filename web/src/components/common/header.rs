use leptos::prelude::*;

/// Page header.
///
/// This component is responsible for pre-styled header rendering.
#[component]
pub fn Header(
    /// Nested children.
    #[prop(optional)]
    children: Option<Children>,
) -> impl IntoView {
    view! { <header class="header">{children.map(|c| c())}</header> }
}
