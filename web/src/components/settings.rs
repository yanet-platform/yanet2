use leptos::prelude::*;

use crate::{
    components::common::{
        checkbox::CheckBox,
        header::Header,
        viewport::{Viewport, ViewportContent},
    },
    settings::SideBarContext,
    theme::{Theme, ThemeContext},
};

#[component]
pub fn SettingsView() -> impl IntoView {
    let theme: ThemeContext = expect_context();
    let sidebar: SideBarContext = expect_context();

    let idx = move || -> usize {
        match theme.current() {
            Theme::Light => 0,
            Theme::Dark => 1,
        }
    };

    let sidebar_idx = move || -> usize {
        match sidebar.is_compact() {
            true => 0,
            false => 1,
        }
    };

    view! {
        <Viewport>
            <Header>
                <h1>"Settings"</h1>
            </Header>

            <ViewportContent>
                <div class="settings">
                    <div class="settings__section">
                        <h3 class="settings__section__head">"Appearance"</h3>

                        <div class="settings__section__item">
                            <label class="settings__section__item-head">"Theme"</label>

                            <div class="settings__section__item-content">
                                <CheckBox
                                    options=["Light", "Dark"].to_vec()
                                    current=idx
                                    on_changed=move |idx| {
                                        match idx {
                                            0 => theme.set(Theme::Light),
                                            1 => theme.set(Theme::Dark),
                                            _ => {}
                                        }
                                    }
                                />
                            </div>
                        </div>

                        <div class="settings__section__item">
                            <label class="settings__section__item-head">"Sidebar width"</label>

                            <div class="settings__section__item-content">
                                <CheckBox
                                    options=["Compact", "Expanded"].to_vec()
                                    current=sidebar_idx
                                    on_changed=move |idx| {
                                        match idx {
                                            0 => sidebar.set_compact(true),
                                            1 => sidebar.set_compact(false),
                                            _ => {}
                                        }
                                    }
                                />
                            </div>
                        </div>
                    </div>

                </div>
            </ViewportContent>
        </Viewport>
    }
}
