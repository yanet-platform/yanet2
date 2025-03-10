use leptos::prelude::*;

#[derive(Clone, Copy)]
pub enum CenterKind {
    Horizontal,
    Vertical,
    Both,
}

impl CenterKind {
    #[inline]
    pub fn as_class(&self) -> &'static str {
        match self {
            Self::Horizontal => "center-h",
            Self::Vertical => "center-v",
            Self::Both => "center",
        }
    }
}

/// Centers nested components.
#[component]
pub fn Center(
    /// Centering type
    #[prop(default = CenterKind::Both)]
    kind: CenterKind,
    /// The components inside the tag which will get rendered.
    children: Children,
) -> impl IntoView {
    view! { <div class=kind.as_class()>{children()}</div> }
}
