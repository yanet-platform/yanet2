//! The scope-name column width. Word wrapping and whitespace
//! normalization live in ync's display helpers, shared with every CLI.

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
        assert_eq!(MIN_NAME_WIDTH, name_width(core::iter::empty()));
    }
}
