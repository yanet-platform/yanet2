use leptos::prelude::*;
use leptos_router::{components::A, hooks::use_location};

use crate::{
    components::common::{
        icons::{Icon, IconKind},
        popover::{Popover, PopoverTrigger},
    },
    settings::SideBarContext,
};

#[component]
pub fn NavHeader(
    /// Name to display.
    name: &'static str,
    /// Icon type.
    icon: IconKind,
) -> impl IntoView {
    view! {
        <div class="nh">
            <div class="nh-logo">
                <div class="nh-logo__icon">
                    <A class:nh-logo__icon__link=true exact=true href="/">
                        <Icon kind=icon />
                    </A>
                </div>
                <div class="nh-logo__name" title=name>
                    <A class:nh-logo__name__link=true exact=true href="/">
                        {name}
                    </A>
                </div>
            </div>
        </div>
    }
}

/// The main navigation menu.
#[component]
pub fn NavMain(
    /// Nested `MenuLink` or `MenuExtLink` components.
    children: Children,
) -> impl IntoView {
    view! { <div class="mm">{children()}</div> }
}

/// The secondary navigation menu.
#[component]
pub fn NavSecondary(
    /// Nested `MenuLink` or `MenuExtLink` components.
    children: Children,
) -> impl IntoView {
    view! { <div class="ms">{children()}</div> }
}

/// Navigation menu footer.
#[component]
pub fn NavFooter(
    /// Nested components.
    children: Children,
) -> impl IntoView {
    view! { <div class="nf">{children()}</div> }
}

#[component]
pub fn MenuLink(
    /// Name to display.
    name: &'static str,
    /// Location this link refers to.
    href: &'static str,
    /// Icon type.
    icon: IconKind,
    /// Exact match criteria for considering this link active.
    ///
    /// If this value is `true`, the link is marked active when the location
    /// matches exactly, otherwise the link is marked active if the current
    /// route starts with it.
    #[prop(default = false)]
    is_exact: bool,
) -> impl IntoView {
    // Subscribe for location changes.
    let location = use_location();
    // Subscribe for sidebar changes.
    //
    // This is required to render popovers in compact mode.
    let ctx: SideBarContext = expect_context();

    // Whether the expected path is fit enough to the actual one.
    let is_active = Memo::new(move |_| {
        let pathname = location.pathname.get();
        let pathname = pathname.to_lowercase();

        let path = href.split(['?', '#']).next().unwrap_or_default().to_lowercase();

        if is_exact {
            pathname == path
        } else {
            pathname.starts_with(&path)
        }
    });

    let icon = move || {
        if ctx.is_compact() {
            view! {
                <Popover class="mi__popover">
                    <PopoverTrigger slot>
                        <div class="mi__icon" title=name>
                            <Icon kind=icon />
                        </div>
                    </PopoverTrigger>
                    {name}
                </Popover>
            }
            .into_any()
        } else {
            view! {
                <div class="mi__icon" title=name>
                    <Icon kind=icon />
                </div>
            }
            .into_any()
        }
    };

    view! {
        <A exact=is_exact href=href class:mi=true class:active=is_active>
            {icon}

            <div class="mi__name" title=name>
                {name}
            </div>
        </A>
    }
}

/// Menu link that refers to some external resource.
///
/// External resources are opened in a separate browser tab.
#[component]
pub fn MenuExtLink(
    /// Name to display.
    name: &'static str,
    /// Location this link refers to.
    href: &'static str,
    /// Icon type.
    icon: IconKind,
) -> impl IntoView {
    view! {
        <a class="mi" href=href target="_blank" rel="noopener noreferrer">
            <div class="mi__icon" title=name>
                <Icon kind=icon />
            </div>
            <div class="mi__name" title=name>
                {name}
            </div>
        </a>
    }
}
