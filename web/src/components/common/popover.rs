//! Popping over help message around HTML element.

use core::time::Duration;
use std::{borrow::Cow, rc::Rc, sync::Mutex};

use leptos::{html::Div, prelude::*, tachys::dom::body};
use leptos_use::{use_element_bounding, UseElementBoundingReturn};

#[component]
pub fn Popover(
    /// User-defined CSS class.
    #[prop(optional, into)]
    class: MaybeProp<String>,
    /// Element that triggers the popover on hovering.
    popover_trigger: PopoverTrigger,
    /// Nested children.
    // TODO: figure out how to use `Children` here.
    children: ChildrenFn,
) -> impl IntoView {
    // Keep active `PopoverContainer` here.
    let popover: Rc<Mutex<Option<PopoverHandle<_>>>> = Rc::new(Mutex::new(None));

    // Create node reference to obtain its bouding rect.
    let div = NodeRef::<Div>::new();

    let UseElementBoundingReturn {
        width,
        height,
        left,
        right,
        top,
        bottom,
        x,
        y,
        ..
    } = use_element_bounding(div);

    let rect = BoundingClientRect {
        height,
        width,
        left,
        right,
        top,
        bottom,
        x,
        y,
    };

    let on_mouse_enter = {
        let popover = popover.clone();
        let children = children.clone();

        move |_ev| {
            let mut lock = popover.lock().expect("must not be poisoned");

            match *lock {
                Some(ref mut handle) => {
                    // There is an active popover. Update its timer, since
                    // we've reentered this element.
                    if let Some(timeout) = handle.timeout {
                        timeout.clear();
                    }
                }
                None => {
                    // No active popover, so mount it.
                    let unmount = {
                        let children = children.clone();

                        mount_to(body(), move || {
                            view! {
                                <PopoverContainer class=class rect=rect>
                                    {children()}
                                </PopoverContainer>
                            }
                        })
                    };

                    *lock = Some(PopoverHandle { _unmount: unmount, timeout: None });
                }
            }
        }
    };
    let on_mouse_leave = move |_ev| {
        let mut handle = popover.lock().expect("must not be poisoned");

        if let Some(ref mut handle) = *handle {
            let timeout = {
                let popover = popover.clone();
                set_timeout_with_handle(
                    move || {
                        // Drop the popover.
                        //
                        // It will be unmounted during dropping.
                        core::mem::drop(popover.lock().expect("must not be poisoned").take())
                    },
                    Duration::from_millis(10),
                )
                .unwrap()
            };

            handle.timeout = Some(timeout);
        }
    };

    let PopoverTrigger { children } = popover_trigger;

    view! {
        <div node_ref=div on:mouseenter=on_mouse_enter on:mouseleave=on_mouse_leave>
            {children()}
        </div>
    }
}

#[slot]
pub struct PopoverTrigger {
    children: Children,
}

struct PopoverHandle<T: Mountable> {
    _unmount: UnmountHandle<T>,
    /// Expire timeout, that is activated on mouse leave event.
    timeout: Option<TimeoutHandle>,
}

#[component]
fn PopoverContainer(
    /// User-defined CSS class.
    #[prop(optional)]
    class: MaybeProp<String>,
    /// Parent bounding rect.
    rect: BoundingClientRect,
    children: Children,
) -> impl IntoView {
    let div = NodeRef::<Div>::new();

    let UseElementBoundingReturn { height, .. } = use_element_bounding(div);

    let transform = move || {
        let dx = rect.right.get();
        let dy = {
            // Center Y of the element this popover attaches to.
            let parent_cy = (rect.bottom.get() + rect.top.get()) / 2.0;

            // Center Y of the popover.
            parent_cy - height.get() / 2.0
        };

        format!("translate3d({:.2}px, {:.2}px, {:.2}px)", dx, dy, 0.0)
    };

    let class = move || -> Cow<'static, str> {
        if let Some(class) = class.get() {
            format!("popover {}", class).into()
        } else {
            "popover".into()
        }
    };

    view! {
        <div node_ref=div class=class style:inset="0px auto auto 0px" style:transform=transform>
            {children()}
        </div>
    }
}

#[derive(Clone, Copy)]
struct BoundingClientRect {
    pub height: Signal<f64>,
    pub width: Signal<f64>,
    pub left: Signal<f64>,
    pub right: Signal<f64>,
    pub top: Signal<f64>,
    pub bottom: Signal<f64>,
    pub x: Signal<f64>,
    pub y: Signal<f64>,
}
