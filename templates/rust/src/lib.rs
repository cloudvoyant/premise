//! Minimal library crate scaffolded from mise-rust-template.
//!
//! Depend on this crate directly from git without crates.io:
//! `mise_lib_template = { git = "https://github.com/<org>/<project>", tag = "vX.Y.Z" }`

/// Returns `true` when `haystack` begins with `needle`.
pub fn starts_with(haystack: &str, needle: &str) -> bool {
    haystack.as_bytes().starts_with(needle.as_bytes())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn starts_with_matches_prefix() {
        assert!(starts_with("hello world", "hello"));
        assert!(!starts_with("hello", "world"));
    }
}
