use leptos::prelude::*;

/// Page header.
///
/// This component is responsible for pre-styled header rendering.
#[component]
pub fn Header(
    /// Optional class.
    #[prop(optional, into)]
    class: Option<&'static str>,
    /// Nested children.
    #[prop(optional)]
    children: Option<Children>,
) -> impl IntoView {
    let mut cls: String = "header".to_string();
    if let Some(c) = class {
        cls.push(' ');
        cls.push_str(c);
    }

    view! { <header class=cls>{children.map(|c| c())}</header> }
}
