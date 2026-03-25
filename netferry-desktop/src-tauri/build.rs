fn main() {
    // Expose the full target triple (e.g. "aarch64-apple-darwin") as a
    // compile-time env var so sidecar resolution can use it at runtime.
    println!(
        "cargo:rustc-env=TARGET_TRIPLE={}",
        std::env::var("TARGET").unwrap()
    );

    // Load NETFERRY_EXPORT_KEY from ../.env if not already set via env.
    // This key is used for AES-256-GCM encryption of exported profiles.
    if std::env::var("NETFERRY_EXPORT_KEY").is_err() {
        if let Ok(content) = std::fs::read_to_string("../.env") {
            for line in content.lines() {
                let line = line.trim();
                if line.starts_with('#') || line.is_empty() {
                    continue;
                }
                if let Some((key, value)) = line.split_once('=') {
                    if key.trim() == "NETFERRY_EXPORT_KEY" {
                        println!("cargo:rustc-env=NETFERRY_EXPORT_KEY={}", value.trim());
                        break;
                    }
                }
            }
        }
    } else {
        println!(
            "cargo:rustc-env=NETFERRY_EXPORT_KEY={}",
            std::env::var("NETFERRY_EXPORT_KEY").unwrap()
        );
    }
    println!("cargo:rerun-if-env-changed=NETFERRY_EXPORT_KEY");
    println!("cargo:rerun-if-changed=../.env");

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
