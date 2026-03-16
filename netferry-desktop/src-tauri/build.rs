fn main() {
    // Expose the full target triple (e.g. "aarch64-apple-darwin") as a
    // compile-time env var so sidecar resolution can use it at runtime.
    println!(
        "cargo:rustc-env=TARGET_TRIPLE={}",
        std::env::var("TARGET").unwrap()
    );
    tauri_build::build()
}
