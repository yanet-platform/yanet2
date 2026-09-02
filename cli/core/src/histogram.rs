//! Histogram arithmetic and unit formatting shared by the metrics CLIs.
//!
//! Everything here works on the wire `commonpb` histogram directly and is
//! pure, so a CLI decides layout and glyphs while every number it prints
//! comes from one place. Bucket counts on the wire are per bucket, not
//! cumulative, and the last bucket is the `+Inf` overflow.

use commonpb::pb::Bucket;

use crate::metrics::format_number;

/// Unit of a metric's values, inferred from its name suffix.
///
/// Prometheus names carry the base unit as their last segment, so
/// `_seconds` and `_bytes` are scaled to a readable magnitude and every
/// other name is printed as a plain number.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Unit {
    Seconds,
    Bytes,
    Plain,
}

impl Unit {
    /// Infers the unit from a metric name.
    pub fn of(name: &str) -> Self {
        if name.ends_with("_seconds") {
            Self::Seconds
        } else if name.ends_with("_bytes") {
            Self::Bytes
        } else {
            Self::Plain
        }
    }

    /// Formats `value` in this unit, with three significant digits below a
    /// hundred and the whole part in full from there on.
    ///
    /// Seconds walk the s/ms/us/ns ladder and bytes the IEC ladder, both
    /// choosing the step after rounding so a value just under a step reads
    /// `1s` or `1KiB` rather than `1000ms` or `1024B`. A plain whole number
    /// gets thousands separators. Bucket bounds are the main input, so
    /// `0.001` seconds must read `1ms`, not `0.001`, and an infinite bound
    /// keeps its sign as `+Inf` or `-Inf` in every unit.
    pub fn format(self, value: f64) -> String {
        if value.is_infinite() {
            return if value > 0.0 { "+Inf" } else { "-Inf" }.to_owned();
        }

        if value.is_nan() {
            return "NaN".to_owned();
        }

        match self {
            Self::Seconds => format_seconds(value),
            Self::Bytes => format_bytes(value),
            Self::Plain => {
                let (sign, magnitude) = sign_and_magnitude(value);

                if magnitude.fract() == 0.0 && magnitude < u64::MAX as f64 {
                    format!("{sign}{}", format_number(magnitude as u64))
                } else {
                    render_readable(round_readable(value))
                }
            }
        }
    }
}

/// Where a histogram's percentile falls.
#[derive(Debug, Clone, Copy, PartialEq)]
pub enum Quantile {
    /// The histogram holds no observations.
    Empty,
    /// Inside the bucket with this upper bound.
    Within(f64),
    /// Inside the overflow bucket, above this last finite bound.
    Above(f64),
    /// Inside an overflow bucket that no finite bound precedes.
    Unbounded,
}

impl Quantile {
    /// Renders the estimate in `unit`: the bucket bound, `>` and the last
    /// finite bound for an overflow, `-` for no observations.
    pub fn render(self, unit: Unit) -> String {
        match self {
            Self::Empty => "-".to_owned(),
            Self::Within(bound) => unit.format(bound),
            Self::Above(bound) => format!(">{}", unit.format(bound)),
            Self::Unbounded => "+Inf".to_owned(),
        }
    }
}

/// Locates the bucket holding the `percent`-th observation.
///
/// The estimate is the bucket's upper bound rather than an interpolated
/// point: exact when every bucket holds a single integer value, as the
/// burst histograms do, a conservative bound otherwise, and never a
/// precision the buckets do not have. The target rank rounds up, so p99 of
/// ten observations is the tenth, and it is computed in integers so a
/// count beyond the float mantissa still lands in the right bucket. The
/// rank is taken over the bucket counts themselves, so a stale total on
/// the wire cannot push it past the last bucket. Only a `+Inf` bound is
/// the overflow: a `-Inf` bound is an ordinary bucket that holds exactly
/// the `-Inf` observations.
pub fn quantile(buckets: &[Bucket], percent: u8) -> Quantile {
    let total: u64 = buckets.iter().map(|bucket| bucket.count).sum();

    if total == 0 {
        return Quantile::Empty;
    }

    let target = (u128::from(total) * u128::from(percent))
        .div_ceil(100)
        .clamp(1, u128::from(total));
    let mut cumulative: u128 = 0;
    let mut last_bound = None;

    for bucket in buckets {
        cumulative += u128::from(bucket.count);

        if cumulative >= target {
            return if is_overflow(bucket.upper_bound) {
                last_bound.map_or(Quantile::Unbounded, Quantile::Above)
            } else {
                Quantile::Within(bucket.upper_bound)
            };
        }

        if !is_overflow(bucket.upper_bound) {
            last_bound = Some(bucket.upper_bound);
        }
    }

    Quantile::Empty
}

/// Whether `bound` closes the overflow bucket that catches every
/// observation above the last real bound.
fn is_overflow(bound: f64) -> bool {
    bound == f64::INFINITY
}

/// One row of an expanded bucket listing.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum BucketRow {
    /// A single bucket, by index.
    Bucket(usize),
    /// A run of consecutive empty buckets, by first and last index.
    EmptyRun { first: usize, last: usize },
}

/// Chooses the rows worth printing for a bucket listing.
///
/// Leading and trailing empty buckets are dropped and an interior run of
/// two or more empty buckets collapses into one row, so a 34-bucket
/// histogram with five populated buckets prints in a handful of lines
/// while still showing where its gaps are. A lone empty bucket stays as
/// is: collapsing it would cost a line, not save one.
pub fn bucket_rows(buckets: &[Bucket]) -> Vec<BucketRow> {
    let populated = || buckets.iter().enumerate().filter(|(_, bucket)| bucket.count > 0);
    let Some((first, _)) = populated().next() else {
        return Vec::new();
    };
    let (last, _) = populated().next_back().expect("a populated bucket exists");

    let mut rows = Vec::new();
    let mut index = first;

    while index <= last {
        if buckets[index].count == 0 {
            let mut end = index;

            while end < last && buckets[end + 1].count == 0 {
                end += 1;
            }

            if end > index {
                rows.push(BucketRow::EmptyRun { first: index, last: end });
                index = end + 1;
                continue;
            }
        }

        rows.push(BucketRow::Bucket(index));
        index += 1;
    }

    rows
}

/// Labels the bucket at `index` by its upper bound.
///
/// The overflow bucket is labelled `>` and the last real bound before it,
/// or `+Inf` when no other bucket precedes it.
pub fn bucket_label(buckets: &[Bucket], index: usize, unit: Unit) -> String {
    let bound = buckets[index].upper_bound;

    if !is_overflow(bound) {
        return unit.format(bound);
    }

    let last_bound = buckets[..index]
        .iter()
        .rev()
        .map(|bucket| bucket.upper_bound)
        .find(|bound| !is_overflow(*bound));

    match last_bound {
        Some(bound) => format!(">{}", unit.format(bound)),
        None => "+Inf".to_owned(),
    }
}

/// Formats `count` as a percentage of `total` with one decimal.
///
/// A non-zero share that rounds to nothing reads `<0.1%` so a populated
/// bucket is never mistaken for an empty one, and a total of zero reads
/// `-` since no share exists.
pub fn format_share(count: u64, total: u64) -> String {
    if total == 0 {
        return "-".to_owned();
    }

    let percent = count as f64 * 100.0 / total as f64;

    if count > 0 && percent < 0.1 {
        return "<0.1%".to_owned();
    }

    if count == total {
        return "100%".to_owned();
    }

    format!("{percent:.1}%")
}

/// Formats `seconds` on the s/ms/us/ns ladder, picking the coarsest step
/// on which the rounded magnitude is at least one.
fn format_seconds(seconds: f64) -> String {
    const LADDER: [(f64, &str); 4] = [(1.0, "s"), (1e-3, "ms"), (1e-6, "us"), (1e-9, "ns")];

    if seconds == 0.0 {
        return "0s".to_owned();
    }

    let (sign, magnitude) = sign_and_magnitude(seconds);

    for (position, (scale, suffix)) in LADDER.iter().enumerate() {
        let scaled = round_readable(magnitude / scale);

        if scaled >= 1.0 || position + 1 == LADDER.len() {
            return format!("{sign}{}{suffix}", render_readable(scaled));
        }
    }

    unreachable!("the last ladder step always matches")
}

/// Formats `bytes` on the IEC ladder, moving up a step whenever the
/// rounded magnitude would reach the next one.
fn format_bytes(bytes: f64) -> String {
    const UNITS: [&str; 5] = ["B", "KiB", "MiB", "GiB", "TiB"];

    let (sign, mut value) = sign_and_magnitude(bytes);
    let mut unit = 0;

    loop {
        let rounded = round_readable(value);

        if rounded < 1024.0 || unit + 1 == UNITS.len() {
            return format!("{sign}{}{}", render_readable(rounded), UNITS[unit]);
        }

        value /= 1024.0;
        unit += 1;
    }
}

/// Splits `value` into the sign to print and its magnitude, so a ladder
/// walks the magnitude and a negative bound keeps its unit.
fn sign_and_magnitude(value: f64) -> (&'static str, f64) {
    if value < 0.0 {
        ("-", -value)
    } else {
        ("", value)
    }
}

/// Number of decimals that show three significant digits below a hundred
/// and none from there on, so the whole part is never cut short.
fn readable_decimals(value: f64) -> usize {
    if value == 0.0 {
        return 0;
    }

    let magnitude = value.abs().log10().floor() as i32;

    (2 - magnitude).max(0) as usize
}

/// Rounds `value` to its readable decimals.
fn round_readable(value: f64) -> f64 {
    let factor = 10f64.powi(readable_decimals(value) as i32);

    (value * factor).round() / factor
}

/// Prints an already rounded `value` without trailing zeros, so `2.5`,
/// `75` and `100` all come out at their natural length.
fn render_readable(value: f64) -> String {
    let decimals = readable_decimals(value);
    let rendered = format!("{value:.decimals$}");

    if rendered.contains('.') {
        rendered.trim_end_matches('0').trim_end_matches('.').to_owned()
    } else {
        rendered
    }
}

#[cfg(test)]
mod test {
    use super::*;

    /// Builds a wire histogram from `(upper_bound, count)` pairs and appends
    /// the overflow bucket with `overflow` observations.
    fn histogram(buckets: &[(f64, u64)], overflow: u64) -> Vec<Bucket> {
        buckets
            .iter()
            .map(|&(upper_bound, count)| Bucket { count, upper_bound })
            .chain(core::iter::once(Bucket {
                count: overflow,
                upper_bound: f64::INFINITY,
            }))
            .collect()
    }

    #[test]
    fn test_unit_of_seconds_suffix() {
        assert_eq!(Unit::Seconds, Unit::of("grpc_server_handling_seconds"));
    }

    #[test]
    fn test_unit_of_bytes_suffix() {
        assert_eq!(Unit::Bytes, Unit::of("memory_arena_free_bytes"));
    }

    #[test]
    fn test_unit_of_other_suffix_is_plain() {
        assert_eq!(Unit::Plain, Unit::of("worker_rx_bursts"));
    }

    #[test]
    fn test_unit_format_seconds_ladder() {
        assert_eq!("1ms", Unit::Seconds.format(0.001));
        assert_eq!("2.5ms", Unit::Seconds.format(0.0025));
        assert_eq!("75ms", Unit::Seconds.format(0.075));
        assert_eq!("1s", Unit::Seconds.format(1.0));
        assert_eq!("1.5s", Unit::Seconds.format(1.5));
        assert_eq!("10s", Unit::Seconds.format(10.0));
        assert_eq!("100us", Unit::Seconds.format(0.0001));
        assert_eq!("250ns", Unit::Seconds.format(0.00000025));
        assert_eq!("0s", Unit::Seconds.format(0.0));
    }

    #[test]
    fn test_unit_format_infinite_keeps_its_sign_in_every_unit() {
        assert_eq!("+Inf", Unit::Seconds.format(f64::INFINITY));
        assert_eq!("-Inf", Unit::Bytes.format(f64::NEG_INFINITY));
        assert_eq!("-Inf", Unit::Plain.format(f64::NEG_INFINITY));
    }

    #[test]
    fn test_unit_format_seconds_rounding_moves_up_a_step() {
        assert_eq!("1s", Unit::Seconds.format(0.99951));
        assert_eq!("1ms", Unit::Seconds.format(0.00099951));
    }

    #[test]
    fn test_unit_format_seconds_keeps_the_whole_part_above_a_hundred() {
        assert_eq!("1234s", Unit::Seconds.format(1234.4));
        assert_eq!("999ms", Unit::Seconds.format(0.9994));
    }

    #[test]
    fn test_unit_format_seconds_negative_keeps_its_unit() {
        assert_eq!("-1s", Unit::Seconds.format(-1.0));
        assert_eq!("-2.5ms", Unit::Seconds.format(-0.0025));
    }

    #[test]
    fn test_unit_format_bytes_ladder() {
        assert_eq!("512B", Unit::Bytes.format(512.0));
        assert_eq!("1KiB", Unit::Bytes.format(1024.0));
        assert_eq!("1.5KiB", Unit::Bytes.format(1536.0));
        assert_eq!("8GiB", Unit::Bytes.format(8_589_934_592.0));
    }

    #[test]
    fn test_unit_format_bytes_rounding_moves_up_a_step() {
        assert_eq!("1KiB", Unit::Bytes.format(1023.9));
        assert_eq!("1023B", Unit::Bytes.format(1023.0));
    }

    #[test]
    fn test_unit_format_bytes_negative_keeps_its_unit() {
        assert_eq!("-2KiB", Unit::Bytes.format(-2048.0));
    }

    #[test]
    fn test_unit_format_bytes_stops_at_the_top_step() {
        assert_eq!("2048TiB", Unit::Bytes.format(2048.0 * 1024f64.powi(4)));
    }

    #[test]
    fn test_unit_format_plain_whole_number_has_separators() {
        assert_eq!("1'234'567", Unit::Plain.format(1_234_567.0));
        assert_eq!("0", Unit::Plain.format(0.0));
    }

    #[test]
    fn test_unit_format_plain_fraction_keeps_three_significant_digits() {
        assert_eq!("0.5", Unit::Plain.format(0.5));
        assert_eq!("12.3", Unit::Plain.format(12.345));
    }

    #[test]
    fn test_unit_format_plain_negative_keeps_separators_and_fractions() {
        assert_eq!("-3", Unit::Plain.format(-3.0));
        assert_eq!("-1'234'567", Unit::Plain.format(-1_234_567.0));
        assert_eq!("-0.25", Unit::Plain.format(-0.25));
    }

    #[test]
    fn test_quantile_empty_histogram() {
        let buckets = histogram(&[(1.0, 0), (2.0, 0)], 0);

        assert_eq!(Quantile::Empty, quantile(&buckets, 50));
    }

    #[test]
    fn test_quantile_first_bucket_holds_low_percentiles() {
        let buckets = histogram(&[(0.001, 63), (0.002, 6)], 0);

        assert_eq!(Quantile::Within(0.001), quantile(&buckets, 50));
        assert_eq!(Quantile::Within(0.001), quantile(&buckets, 90));
        assert_eq!(Quantile::Within(0.002), quantile(&buckets, 99));
    }

    #[test]
    fn test_quantile_target_rank_rounds_up() {
        // Ten observations, nine in the first bucket: p90 is the ninth
        // observation and still inside it, p91 is the tenth and beyond.
        let buckets = histogram(&[(1.0, 9), (2.0, 1)], 0);

        assert_eq!(Quantile::Within(1.0), quantile(&buckets, 90));
        assert_eq!(Quantile::Within(2.0), quantile(&buckets, 91));
    }

    #[test]
    fn test_quantile_rank_is_exact_beyond_the_float_mantissa() {
        // 2^53 observations: the exact p99 rank is 8'917'127'262'193'583,
        // one more than a float computation gives, and that one lands in
        // the second bucket.
        let first = 8_917_127_262_193_582;
        let buckets = histogram(&[(1.0, first), (2.0, (1u64 << 53) - first)], 0);

        assert_eq!(Quantile::Within(2.0), quantile(&buckets, 99));
    }

    #[test]
    fn test_quantile_negative_infinity_is_an_ordinary_bucket() {
        let buckets = histogram(&[(f64::NEG_INFINITY, 3), (1.0, 1)], 0);

        assert_eq!(Quantile::Within(f64::NEG_INFINITY), quantile(&buckets, 50));
        assert_eq!("-Inf", quantile(&buckets, 50).render(Unit::Seconds));
    }

    #[test]
    fn test_quantile_overflow_reports_last_finite_bound() {
        let buckets = histogram(&[(1.0, 1), (5.0, 0)], 9);

        assert_eq!(Quantile::Above(5.0), quantile(&buckets, 99));
    }

    #[test]
    fn test_quantile_overflow_without_finite_bound_is_unbounded() {
        let buckets = histogram(&[], 3);

        assert_eq!(Quantile::Unbounded, quantile(&buckets, 50));
    }

    #[test]
    fn test_quantile_render_per_unit() {
        assert_eq!("5ms", Quantile::Within(0.005).render(Unit::Seconds));
        assert_eq!(">10s", Quantile::Above(10.0).render(Unit::Seconds));
        assert_eq!("-", Quantile::Empty.render(Unit::Plain));
        assert_eq!("+Inf", Quantile::Unbounded.render(Unit::Plain));
    }

    #[test]
    fn test_bucket_rows_trims_edges_and_collapses_runs() {
        let buckets = histogram(
            &[(0.0, 0), (1.0, 5), (2.0, 0), (3.0, 0), (4.0, 0), (5.0, 1), (6.0, 0)],
            0,
        );

        assert_eq!(
            vec![
                BucketRow::Bucket(1),
                BucketRow::EmptyRun { first: 2, last: 4 },
                BucketRow::Bucket(5),
            ],
            bucket_rows(&buckets)
        );
    }

    #[test]
    fn test_bucket_rows_keeps_single_interior_empty_bucket() {
        let buckets = histogram(&[(1.0, 5), (2.0, 0), (3.0, 1)], 0);

        assert_eq!(
            vec![BucketRow::Bucket(0), BucketRow::Bucket(1), BucketRow::Bucket(2)],
            bucket_rows(&buckets)
        );
    }

    #[test]
    fn test_bucket_rows_populated_overflow_is_kept() {
        let buckets = histogram(&[(1.0, 5), (2.0, 0)], 2);

        assert_eq!(
            vec![BucketRow::Bucket(0), BucketRow::Bucket(1), BucketRow::Bucket(2)],
            bucket_rows(&buckets)
        );
    }

    #[test]
    fn test_bucket_rows_single_populated_bucket() {
        let buckets = histogram(&[(1.0, 0), (2.0, 7), (3.0, 0)], 0);

        assert_eq!(vec![BucketRow::Bucket(1)], bucket_rows(&buckets));
    }

    #[test]
    fn test_bucket_rows_empty_histogram_has_no_rows() {
        let buckets = histogram(&[(1.0, 0), (2.0, 0)], 0);

        assert!(bucket_rows(&buckets).is_empty());
    }

    #[test]
    fn test_bucket_label_finite_and_overflow() {
        let buckets = histogram(&[(0.005, 1), (0.01, 1)], 1);

        assert_eq!("5ms", bucket_label(&buckets, 0, Unit::Seconds));
        assert_eq!(">10ms", bucket_label(&buckets, 2, Unit::Seconds));
    }

    #[test]
    fn test_bucket_label_negative_infinity_keeps_its_sign() {
        let buckets = histogram(&[(f64::NEG_INFINITY, 1), (0.5, 1)], 1);

        assert_eq!("-Inf", bucket_label(&buckets, 0, Unit::Plain));
        assert_eq!(">0.5", bucket_label(&buckets, 2, Unit::Plain));
    }

    #[test]
    fn test_bucket_label_overflow_without_finite_bound() {
        let buckets = histogram(&[], 1);

        assert_eq!("+Inf", bucket_label(&buckets, 0, Unit::Plain));
    }

    #[test]
    fn test_format_share_cases() {
        assert_eq!("91.3%", format_share(63, 69));
        assert_eq!("100%", format_share(24, 24));
        assert_eq!("<0.1%", format_share(1, 10_000));
        assert_eq!("0.0%", format_share(0, 10));
        assert_eq!("-", format_share(0, 0));
    }
}
