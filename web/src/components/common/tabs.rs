use leptos::prelude::*;
use leptos_router::{components::A, hooks::use_location};

#[component]
pub fn TabNav(
    /// Nested `TabNavLink` components.
    children: Children,
) -> impl IntoView {
    view! { <div class="tab-nav">{children()}</div> }
}

#[component]
pub fn TabNavLink(
    /// Name to display.
    name: &'static str,
    /// Location this link refers to.
    href: &'static str,
) -> impl IntoView {
    // Subscribe for location changes.
    let location = use_location();

    // Whether the expected path is fit enough to the actual one.
    let is_active = Memo::new(move |_| {
        let pathname = location.pathname.get();
        let pathname = pathname.to_lowercase();

        let path = href.split(['?', '#']).next().unwrap_or_default().to_lowercase();

        pathname.starts_with(&path)
    });

    view! {
        <A href=href class:tab-nav-link=true class:active=is_active>
            <div title=name>
                {name}
            </div>
        </A>
    }
}
