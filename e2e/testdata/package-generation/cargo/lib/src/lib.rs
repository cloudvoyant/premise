pub fn message() -> &'static str {
    "hello"
}

#[test]
fn returns_message() {
    assert_eq!(message(), "hello");
}
