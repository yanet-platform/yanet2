use leptos::prelude::window;
use web_sys::Navigator;

pub fn accesskey(key: char) -> Option<String> {
    let nav = window().navigator();
    let platform = Platform::from_navigator(&nav)?;
    let browser = BrowserName::from_navigator(&nav)?;

    match (platform, browser) {
        (Platform::Windows, ..) | (Platform::Linux, ..) => Some(format!("alt+shift+{key}")),
        (Platform::Mac, ..) => Some(format!("⌃⌥{key}")),
    }
}

#[derive(Debug)]
enum Platform {
    Windows,
    Mac,
    Linux,
}

impl Platform {
    fn from_navigator(nav: &Navigator) -> Option<Self> {
        let platform = nav.platform().ok()?;

        if platform.contains("Win") {
            Some(Self::Windows)
        } else if platform.contains("Mac") {
            Some(Self::Mac)
        } else if platform.contains("Linux") {
            Some(Self::Linux)
        } else {
            None
        }
    }
}

#[derive(Debug)]
enum BrowserName {
    Chrome,
    Firefox,
    Safari,
}

impl BrowserName {
    fn from_navigator(nav: &Navigator) -> Option<Self> {
        let user_agent = nav.user_agent().ok()?;

        if user_agent.contains("Chrome") {
            Some(Self::Chrome)
        } else if user_agent.contains("Firefox") {
            Some(Self::Firefox)
        } else if user_agent.contains("Safari") {
            Some(Self::Safari)
        } else {
            None
        }
    }
}
