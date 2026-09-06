use mise_lib_template::starts_with;

fn main() {
    // --version resolves to CARGO_PKG_VERSION, which Cargo fills from
    // [package] version in Cargo.toml (kept in sync with version.txt).
    if std::env::args().any(|a| a == "--version" || a == "-V") {
        println!("mise_lib_template {}", env!("CARGO_PKG_VERSION"));
        return;
    }

    println!("Hello from mise-lib-template!");
    println!(
        "starts_with(\"hello\", \"he\"): {}",
        starts_with("hello", "he"),
    );
}
