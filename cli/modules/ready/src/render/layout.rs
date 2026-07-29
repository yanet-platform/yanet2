//! Text-layout helpers: the scope-name column width and whitespace
//! normalization. Word wrapping itself lives in `ync::display::wrap_words`,
//! shared with the core CLI's own output helpers.

const MIN_NAME_WIDTH: usize = 12;
const MAX_NAME_WIDTH: usize = 40;

/// Computes the scope-name column width for a rendered set of scopes.
///
/// The longest name wins, clamped to `[MIN_NAME_WIDTH, MAX_NAME_WIDTH]`.
/// Compute this once per render (or once per watch session) and hold it —
/// recomputing per row would make the column width jitter.
pub fn name_width<'a>(names: impl IntoIterator<Item = &'a str>) -> usize {
    names
        .into_iter()
        .map(str::len)
        .max()
        .unwrap_or(MIN_NAME_WIDTH)
        .clamp(MIN_NAME_WIDTH, MAX_NAME_WIDTH)
}

/// Collapses every run of whitespace in `text` — including embedded
/// newlines — into a single ASCII space.
///
/// Used on the non-wrapping (non-TTY) render path, where reason text is
/// printed as one grep-friendly line; the source message (a Go
/// `err.Error()`) may otherwise carry embedded newlines.
pub fn normalize_whitespace(text: &str) -> String {
    text.split_whitespace().collect::<Vec<_>>().join(" ")
}

#[cfg(test)]
mod test {
    use super::*;

    #[test]
    fn name_width_clamps_to_minimum() {
        assert_eq!(MIN_NAME_WIDTH, name_width(["rib"]));
    }

    #[test]
    fn name_width_clamps_to_maximum() {
        let long_name = "a".repeat(100);
        assert_eq!(MAX_NAME_WIDTH, name_width([long_name.as_str()]));
    }

    #[test]
    fn name_width_uses_longest_name() {
        assert_eq!(16, name_width(["bird-session", "fib:gw-01:route0", "rib"]));
    }

    #[test]
    fn name_width_empty_defaults_to_minimum() {
        assert_eq!(MIN_NAME_WIDTH, name_width(std::iter::empty()));
    }

    #[test]
    fn normalize_whitespace_collapses_embedded_newlines() {
        assert_eq!(
            "rpc error: connection refused extra detail",
            normalize_whitespace("rpc error: connection refused\nextra detail")
        );
    }

    #[test]
    fn normalize_whitespace_collapses_runs_of_spaces() {
        assert_eq!("a b", normalize_whitespace("a    b"));
    }

    #[test]
    fn normalize_whitespace_empty_text_returns_empty() {
        assert_eq!("", normalize_whitespace(""));
    }
}
