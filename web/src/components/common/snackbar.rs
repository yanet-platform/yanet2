use core::{error::Error, fmt::Display, time::Duration};
use std::{borrow::Cow, collections::VecDeque};

use instant::Instant;
use leptos::{ev::MouseEvent, leptos_dom::helpers::TimeoutHandle, prelude::*};

use crate::{
    components::common::icons::{Icon, IconKind},
    xxx::str::IntoTitle,
};

const DEFAULT_HEADER: &str = "Something went wrong";
const DEFAULT_SHOW_DURATION: Duration = Duration::from_secs(3);

/// Semantic meaning of a snackbar.
#[derive(Debug, Clone, Copy)]
pub enum SnackbarKind {
    /// Should be set when some error occurred.
    Error,
}

impl SnackbarKind {
    /// Returns the CSS class for this kind.
    #[inline]
    pub fn as_class(&self) -> &'static str {
        match self {
            Self::Error => "sb-k-e",
        }
    }

    /// Returns an icon kind for this kind.
    #[inline]
    pub fn as_icon_kind(&self) -> IconKind {
        match self {
            Self::Error => IconKind::ErrorTriangle,
        }
    }
}

/// Snackbar data used for displaying.
#[derive(Debug, Clone)]
pub struct SnackbarData {
    kind: SnackbarKind,
    header: Option<Cow<'static, str>>,
    description: Cow<'static, str>,
}

impl SnackbarData {
    pub fn error(description: impl Into<Cow<'static, str>>) -> Self {
        Self {
            kind: SnackbarKind::Error,
            header: Some(Cow::Borrowed("Error")),
            description: description.into(),
        }
    }
}

impl From<Box<dyn Error>> for SnackbarData {
    fn from(err: Box<dyn Error>) -> Self {
        Self {
            kind: SnackbarKind::Error,
            header: None,
            description: format!("{}", err).into(),
        }
    }
}

/// Snackbars context.
///
/// Provided with `use_context` and it used to push notifications to be
/// displayed for some period of time.
/// The most common usage is to push important error messages, like failed API
/// calls.
#[derive(Copy, Clone)]
pub struct SnackbarContext {
    snackbars: SnackbarStorage,
}

impl SnackbarContext {
    #[inline]
    const fn new(snackbars: SnackbarStorage) -> Self {
        Self { snackbars }
    }

    /// Pushes a new nofitication to the snackbar stack.
    pub fn push<T>(&self, ev: T)
    where
        T: Into<SnackbarData>,
    {
        self.snackbars.push(ev.into())
    }

    // TODO: docs.
    pub fn error<E>(&self, err: E)
    where
        E: Display,
    {
        self.push(SnackbarData::error(format!("{err}")));
    }
}

#[derive(Debug, Clone)]
struct Data {
    id: u64,
    timeout: Option<TimeoutHandle>,
    created_at: Instant,
    timer_tick: ReadSignal<()>,
    set_timer_tick: WriteSignal<()>,
    data: SnackbarData,
}

#[derive(Clone, Copy)]
struct SnackbarStorage {
    show_duration: Duration,
    /// Current auto-incremented ID.
    ///
    /// Used to track currently active snackbars, primarily for maintaining
    /// the internal state, like closing unwanted bars, or disabling timers
    /// on mouse events.
    id: RwSignal<u64>,
    bars: ReadSignal<VecDeque<Data>>,
    set_bars: WriteSignal<VecDeque<Data>>,
}

impl SnackbarStorage {
    pub fn new(show_duration: Duration) -> Self {
        let id: RwSignal<u64> = RwSignal::new(0);
        let (bars, set_bars) = signal(VecDeque::new());

        Self { show_duration, id, bars, set_bars }
    }

    pub fn push(&self, data: SnackbarData) {
        self.id.update(|id| {
            let timeout = {
                let id = *id;
                let this = *self;
                set_timeout_with_handle(move || this.remove(id), self.show_duration).unwrap()
            };

            let (timer_tick, set_timer_tick) = signal(());
            let data = Data {
                id: *id,
                timeout: Some(timeout),
                created_at: Instant::now(),
                timer_tick,
                set_timer_tick,
                data,
            };

            self.set_bars.update(|bars| {
                bars.push_back(data);
            });
            self.start_tick_timer(*id);
            *id += 1;
        });
    }

    pub fn remove(&self, id: u64) {
        self.set_bars.update(|bars| {
            if let Some(idx) = bars.iter().position(|sb| sb.id == id) {
                bars.remove(idx);
            }
        });
    }

    pub fn stop_autoremove_timer(&self, id: u64) {
        self.update_bar(id, |bar| {
            if let Some(timer) = bar.timeout.take() {
                timer.clear();
            }
        });
    }

    pub fn restart_autoremove_timer(&self, id: u64) {
        self.update_bar(id, |bar| {
            if let Some(timer) = bar.timeout.take() {
                timer.clear();
            }

            let timeout = {
                let this = *self;
                set_timeout_with_handle(move || this.remove(id), self.show_duration).unwrap()
            };
            bar.timeout = Some(timeout);
        });
    }

    fn update_bar<F>(&self, id: u64, f: F)
    where
        F: FnOnce(&mut Data),
    {
        self.set_bars.update(|bars| {
            if let Some(sb) = bars.iter_mut().find(|sb| sb.id == id) {
                f(sb);
            }
        });
    }

    fn start_tick_timer(self, id: u64) {
        set_timeout(
            move || {
                self.bars.with(|bars| {
                    if let Some(bar) = bars.iter().find(|sb: &&Data| sb.id == id) {
                        // Trigger snackbars to rerender its "N secs ago" message.
                        bar.set_timer_tick.set(());

                        // Schedule the next timer tick.
                        self.start_tick_timer(id);
                    }
                });
            },
            Duration::from_secs(1),
        )
    }
}

/// Renders a snackbar component, i.e. a component that appears on some
/// external event for a short period of time.
///
/// For example, when an API request fails for some reason it will display the
/// error in the bottom-right (by default) window corner.
#[component]
fn SnackbarView<F, E, L>(
    /// Snackbar data.
    sb: Data,
    /// Called on close button click.
    ///
    /// Probably it's time to remove this bar from the stack.
    on_close: F,
    /// Called on mouse enter.
    ///
    /// Can be used for canceling the auto-remove timer.
    on_mouseenter: E,
    /// Called on mouse leave.
    ///
    /// Can be used for restarting the auto-remove timer.
    on_mouseleave: L,
) -> impl IntoView
where
    F: Fn() + 'static,
    E: Fn(MouseEvent) + 'static,
    L: Fn(MouseEvent) + 'static,
{
    let created_ago = move || {
        sb.timer_tick.get();
        let elapsed = sb.created_at.elapsed();

        // Show precise information about event's birth time if there is custom show
        // duration set.
        if elapsed <= DEFAULT_SHOW_DURATION {
            "just now".to_string()
        } else {
            format!("{}s ago", elapsed.as_secs())
        }
    };

    let header = sb.data.header.unwrap_or(DEFAULT_HEADER.into());
    let on_click = move |ev: MouseEvent| {
        ev.prevent_default();
        on_close();
    };

    view! {
        <div
            class=format!("sb sb-a__appearing sb-a__show {}", sb.data.kind.as_class())
            on:mouseenter=on_mouseenter
            on:mouseleave=on_mouseleave
        >
            <div class="sb__header">
                <div class="sb-h__box">
                    <div class="sb-h__icon">
                        <Icon kind=sb.data.kind.as_icon_kind() />
                    </div>

                    <strong class="sb-h__data">{header}</strong>

                    <small class="sb-h__time">{move || created_ago()}</small>
                </div>
                <button class="sb-h__button" on:click=on_click>
                    <Icon kind=IconKind::Cross />
                </button>
            </div>
            <div class="sb__content">{sb.data.description.into_title()}</div>
        </div>
    }
}

#[component]
fn SnackbarStack(
    /// Snackbars storage.
    snackbars: SnackbarStorage,
) -> impl IntoView {
    view! {
        <div class="sbs pos-br">
            <For
                each=move || snackbars.bars.get()
                key=|sb| sb.id
                children=move |sb| {
                    let id = sb.id;

                    view! {
                        <SnackbarView
                            sb=sb
                            on_close=move || snackbars.remove(id)
                            on_mouseenter=move |ev| {
                                ev.prevent_default();
                                snackbars.stop_autoremove_timer(id);
                            }
                            on_mouseleave=move |ev| {
                                ev.prevent_default();
                                snackbars.restart_autoremove_timer(id);
                            }
                        />
                    }
                }
            />
        </div>
    }
}

/// Component that activates push notification functionality using snackbars.
///
/// Snackbars (or toasts) provide a lightweight and customizable alert messages
/// appearred on some event for a short period of time.
///
/// This component is capable of displaying multiple snackbars at a time.
#[component]
pub fn Snackbar(
    /// Period of time during which snackbars are shown.
    ///
    /// Mouse enter events on a specific snackbar prevent it from being hidden,
    /// while mouse leave events restart the timer.
    #[prop(default = DEFAULT_SHOW_DURATION)]
    show_duration: Duration,
    /// The components inside the tag which will get rendered.
    children: Children,
) -> impl IntoView {
    let snackbars = SnackbarStorage::new(show_duration);
    provide_context(SnackbarContext::new(snackbars));

    view! { <>{children()} <SnackbarStack snackbars=snackbars /></> }
}
