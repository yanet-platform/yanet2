use core::fmt::Display;

pub trait IntoTitle {
    fn into_title(self) -> String;
}

impl<T> IntoTitle for T
where
    T: Display,
{
    fn into_title(self) -> String {
        let v = format!("{}", self);

        if v.is_empty() {
            return v;
        }

        if !v.is_ascii() {
            return v;
        }

        let (head, tail) = v.split_at(1);
        head.to_uppercase() + tail
    }
}
