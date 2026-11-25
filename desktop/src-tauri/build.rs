fn main() {
    // Generate `allow-<command>` permissions for the app's own commands so
    // they must be granted explicitly (only the local connect page gets them).
    tauri_build::try_build(
        tauri_build::Attributes::new()
            .app_manifest(tauri_build::AppManifest::new().commands(&["startup", "connect"])),
    )
    .expect("failed to run tauri-build");
}
