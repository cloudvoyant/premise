use premise::starts_with;

fn main() {
    // --version resolves to CARGO_PKG_VERSION, which Cargo fills from
    // [package] version in Cargo.toml (kept in sync with version.txt).
    if std::env::args().any(|a| a == "--version" || a == "-V") {
        println!("premise {}", env!("CARGO_PKG_VERSION"));
        return;
    }

    println!("Hello from premise!");
    println!(
        "starts_with(\"hello\", \"he\"): {}",
        starts_with("hello", "he"),
    );
}
