fn main() {
    // Expose the full target triple (e.g. "aarch64-apple-darwin") as a
    // compile-time env var so sidecar resolution can use it at runtime.
    println!(
        "cargo:rustc-env=TARGET_TRIPLE={}",
        std::env::var("TARGET").unwrap()
    );

    // On macOS: compile the Objective-C SMAppService wrapper so the main app
    // can register / query the privileged helper daemon without pulling in
    // a full ObjC binding crate.
    #[cfg(target_os = "macos")]
    {
        cc::Build::new()
            .file("src/smappservice.m")
            .flag("-fobjc-arc")
            .compile("smappservice");

        println!("cargo:rustc-link-lib=framework=ServiceManagement");
        println!("cargo:rustc-link-lib=framework=Foundation");
    }

    tauri_build::build()
}
