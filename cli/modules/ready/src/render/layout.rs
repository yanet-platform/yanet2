//! Text-layout helpers: the scope-name column width and word wrapping.

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

/// Greedily wraps `text` into lines of at most `width` columns, breaking
/// only at whitespace.
///
/// A word longer than `width` is kept whole on its own (overflowing) line
/// rather than split. Returns `text` unchanged as a single line when
/// `width` is `0`.
pub fn wrap_words(text: &str, width: usize) -> Vec<String> {
    if width == 0 {
        return vec![text.to_string()];
    }

    let mut lines = Vec::new();
    let mut current = String::new();

    for word in text.split_whitespace() {
        if current.is_empty() {
            current.push_str(word);
        } else if current.len() + 1 + word.len() <= width {
            current.push(' ');
            current.push_str(word);
        } else {
            lines.push(std::mem::take(&mut current));
            current.push_str(word);
        }
    }

    if !current.is_empty() || lines.is_empty() {
        lines.push(current);
    }

    lines
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
    fn wrap_words_fits_within_width() {
        assert_eq!(vec!["aa bb".to_string(), "cc".to_string()], wrap_words("aa bb cc", 5));
    }

    #[test]
    fn wrap_words_keeps_long_word_whole() {
        assert_eq!(
            vec!["superlongword".to_string(), "short".to_string()],
            wrap_words("superlongword short", 5)
        );
    }

    #[test]
    fn wrap_words_zero_width_returns_single_line() {
        assert_eq!(vec!["one long line".to_string()], wrap_words("one long line", 0));
    }

    #[test]
    fn wrap_words_empty_text_returns_one_empty_line() {
        assert_eq!(vec![String::new()], wrap_words("", 10));
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
