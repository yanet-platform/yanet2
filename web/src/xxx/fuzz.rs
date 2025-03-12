pub fn fuzz_match(search: &str, mut target: &str) -> bool {
    if target.len() < search.len() {
        return false;
    }
    if search == target {
        return true;
    }

    'outer: for c1 in search.chars() {
        for (idx, c2) in target.chars().enumerate() {
            if c1 == c2 {
                target = &target[idx + c2.len_utf8()..];
                continue 'outer;
            }
        }

        return false;
    }

    true
}
