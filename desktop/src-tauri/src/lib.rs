// Coffer desktop: a native shell around a self-hosted Coffer server.
//
// The window starts on a small bundled "connect" page (tauri://localhost). Once
// a server is chosen the same webview navigates to it. The remote origin never
// gets Tauri IPC: the only capability (capabilities/connect-page.json) is local,
// and every command below also refuses calls that do not come from the bundled
// page.

use std::collections::{HashMap, HashSet, VecDeque};
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicU32, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use serde::Serialize;
use tauri::menu::{AboutMetadata, Menu, MenuEvent, MenuItem, PredefinedMenuItem, Submenu};
#[cfg(target_os = "macos")]
use tauri::menu::{HELP_SUBMENU_ID, WINDOW_SUBMENU_ID};
use tauri::webview::{DownloadEvent, NewWindowResponse};
use tauri::{
    AppHandle, Manager, Runtime, Url, Webview, WebviewUrl, WebviewWindow, WebviewWindowBuilder,
};
use tauri_plugin_notification::NotificationExt;
use tauri_plugin_opener::OpenerExt;
use tauri_plugin_store::StoreExt;

const MAIN: &str = "main";
const APP_NAME: &str = "Coffer";
const STORE_FILE: &str = "settings.json";
const KEY_SERVER: &str = "serverUrl";
const KEY_LAST: &str = "lastServerUrl";
const CONNECT_PAGE: &str = "index.html";

const MENU_CHANGE_SERVER: &str = "change-server";
const MENU_RELOAD: &str = "reload";
const MENU_QUIT: &str = "quit";
const MENU_ZOOM_IN: &str = "zoom-in";
const MENU_ZOOM_OUT: &str = "zoom-out";
const MENU_ZOOM_RESET: &str = "zoom-reset";

const ZOOM_STEPS: &[f64] = &[
    0.5, 0.67, 0.75, 0.8, 0.9, 1.0, 1.1, 1.25, 1.5, 1.75, 2.0, 2.5, 3.0,
];

/// Shared app state.
#[derive(Default)]
struct AppState {
    /// The configured server. Navigation is only allowed inside its origin.
    server: Arc<Mutex<Option<Url>>>,
    /// Zoom level per window label.
    zoom: Mutex<HashMap<String, f64>>,
    downloads: Arc<Mutex<Downloads>>,
    viewer_seq: AtomicU32,
}

#[derive(Default)]
struct Downloads {
    /// Destination paths by download URL, in request order.
    pending: HashMap<String, VecDeque<PathBuf>>,
    /// Paths handed out but not finished yet, so parallel downloads of the
    /// same name do not collide.
    reserved: HashSet<PathBuf>,
}

// ---------------------------------------------------------------------------
// URLs and navigation policy
// ---------------------------------------------------------------------------

/// The URL of the bundled connect page.
fn connect_page_url() -> Url {
    #[cfg(any(windows, target_os = "android"))]
    let base = "http://tauri.localhost/";
    #[cfg(not(any(windows, target_os = "android")))]
    let base = "tauri://localhost/";
    Url::parse(base).unwrap().join(CONNECT_PAGE).unwrap()
}

/// True for the bundled app assets (the connect page).
fn is_local(url: &Url) -> bool {
    // Must match connect_page_url: only the scheme this platform serves the
    // bundled page from counts, so a server page can't pass as local.
    #[cfg(any(windows, target_os = "android"))]
    let local = url.scheme() == "http" && url.host_str() == Some("tauri.localhost");
    #[cfg(not(any(windows, target_os = "android")))]
    let local = url.scheme() == "tauri" && url.host_str() == Some("localhost");
    local
}

fn same_origin(a: &Url, b: &Url) -> bool {
    a.origin() == b.origin()
}

fn is_server_url(server: &Mutex<Option<Url>>, url: &Url) -> bool {
    matches!(url.scheme(), "http" | "https")
        && server
            .lock()
            .unwrap()
            .as_ref()
            .is_some_and(|s| same_origin(s, url))
}

/// Opens a link in the user's browser / mail client.
fn open_external<R: Runtime>(app: &AppHandle<R>, url: &Url) {
    if matches!(url.scheme(), "http" | "https" | "mailto") {
        if let Err(e) = app.opener().open_url(url.as_str(), None::<&str>) {
            eprintln!("coffer: could not open {url}: {e}");
        }
    }
}

/// Decides whether the webview may navigate to `url`. Anything outside the
/// server's origin (and the bundled page) is opened in the system browser.
fn allow_navigation<R: Runtime>(
    app: &AppHandle<R>,
    server: &Mutex<Option<Url>>,
    url: &Url,
) -> bool {
    if is_local(url) {
        return true;
    }
    match url.scheme() {
        "http" | "https" => {
            if is_server_url(server, url) {
                true
            } else {
                open_external(app, url);
                false
            }
        }
        // In-page documents (iframes, previews, generated downloads).
        "about" | "blob" | "data" => true,
        "mailto" => {
            open_external(app, url);
            false
        }
        _ => false,
    }
}

/// Turns what the user typed into a base URL, e.g. "192.168.1.20:8080" ->
/// "http://192.168.1.20:8080/" and "files.example.com" -> "https://files.example.com/".
fn normalize_server_url(input: &str) -> Result<Url, String> {
    let s = input.trim();
    if s.is_empty() {
        return Err("Enter your server's address.".into());
    }
    let with_scheme = if s.contains("://") {
        s.to_string()
    } else {
        let authority = s.split(['/', '?', '#']).next().unwrap_or(s);
        let host = authority
            .rsplit_once(':')
            .filter(|(_, port)| port.chars().all(|c| c.is_ascii_digit()))
            .map(|(h, _)| h)
            .unwrap_or(authority);
        let has_port = host.len() != authority.len();
        let looks_local = has_port
            || host == "localhost"
            || host.parse::<std::net::Ipv4Addr>().is_ok()
            || host.starts_with('[')
            || !host.contains('.')
            || [".local", ".lan", ".home", ".internal", ".home.arpa"]
                .iter()
                .any(|suffix| host.ends_with(suffix));
        format!("{}://{}", if looks_local { "http" } else { "https" }, s)
    };
    let mut url = Url::parse(&with_scheme)
        .map_err(|_| "That doesn't look like a valid address.".to_string())?;
    if !matches!(url.scheme(), "http" | "https") {
        return Err("Use an http:// or https:// address.".into());
    }
    if url.host_str().is_none_or(str::is_empty) {
        return Err("That doesn't look like a valid address.".into());
    }
    if !url.username().is_empty() || url.password().is_some() {
        return Err("Leave the username and password out of the address.".into());
    }
    url.set_query(None);
    url.set_fragment(None);
    let path = url.path().trim_end_matches('/').to_string();
    url.set_path(&format!("{path}/"));
    Ok(url)
}

fn display_url(url: &Url) -> String {
    url.as_str().trim_end_matches('/').to_string()
}

// ---------------------------------------------------------------------------
// Server check
// ---------------------------------------------------------------------------

/// Fetches `<base>/api/config` and checks it is a Coffer server. Returns the
/// effective base URL (after redirects, e.g. http -> https) and the app name.
async fn check_server(base: &Url) -> Result<(Url, String), String> {
    let config_url = base.join("api/config").map_err(|e| e.to_string())?;
    let host = base.host_str().unwrap_or_default().to_string();
    let client = reqwest::Client::builder()
        .user_agent(concat!("Coffer-Desktop/", env!("CARGO_PKG_VERSION")))
        .connect_timeout(Duration::from_secs(6))
        .timeout(Duration::from_secs(12))
        .build()
        .map_err(|e| e.to_string())?;

    let resp = client
        .get(config_url)
        .header(reqwest::header::ACCEPT, "application/json")
        .send()
        .await
        .map_err(|e| {
            if e.is_timeout() {
                format!("{host} took too long to respond.")
            } else if e.is_connect() {
                format!("Couldn't reach {host}. Check the address and that the server is running.")
            } else {
                format!("Couldn't connect to {host}: {}", root_cause(&e))
            }
        })?;

    let status = resp.status();
    if !status.is_success() {
        return Err(format!(
            "{host} answered with HTTP {}. Is this a Coffer server?",
            status.as_u16()
        ));
    }

    // Follow redirects: derive the base from the final URL of /api/config.
    let final_url = resp.url().clone();
    let effective = match final_url.path().strip_suffix("api/config") {
        Some(prefix) => {
            let mut u = final_url.clone();
            u.set_path(prefix);
            u.set_query(None);
            u.set_fragment(None);
            u
        }
        None => base.clone(),
    };

    let not_coffer = || "That address doesn't look like a Coffer server.".to_string();
    let body: serde_json::Value = resp.json().await.map_err(|_| not_coffer())?;
    let app_name = body
        .get("appName")
        .and_then(|v| v.as_str())
        .ok_or_else(not_coffer)?
        .trim()
        .to_string();
    Ok((
        effective,
        if app_name.is_empty() {
            APP_NAME.into()
        } else {
            app_name
        },
    ))
}

fn root_cause(err: &dyn std::error::Error) -> String {
    let mut cur = err;
    while let Some(next) = cur.source() {
        cur = next;
    }
    cur.to_string()
}

// ---------------------------------------------------------------------------
// Commands (connect page only)
// ---------------------------------------------------------------------------

fn ensure_local<R: Runtime>(webview: &Webview<R>) -> Result<(), String> {
    match webview.url() {
        Ok(url) if is_local(&url) && webview.label() == MAIN => Ok(()),
        _ => Err("not allowed".into()),
    }
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct Startup {
    saved_url: Option<String>,
    last_url: Option<String>,
    version: String,
}

fn store_string<R: Runtime>(app: &AppHandle<R>, key: &str) -> Option<String> {
    app.store(STORE_FILE)
        .ok()?
        .get(key)
        .and_then(|v| v.as_str().map(str::to_string))
}

/// What the connect page should do on load.
#[tauri::command]
fn startup<R: Runtime>(app: AppHandle<R>, webview: Webview<R>) -> Result<Startup, String> {
    ensure_local(&webview)?;
    let clean = |s: String| Url::parse(&s).map(|u| display_url(&u)).ok();
    Ok(Startup {
        saved_url: store_string(&app, KEY_SERVER).and_then(clean),
        last_url: store_string(&app, KEY_LAST).and_then(clean),
        version: app.package_info().version.to_string(),
    })
}

/// Validates the server, saves it and navigates the window to it.
#[tauri::command]
async fn connect<R: Runtime>(
    app: AppHandle<R>,
    webview: Webview<R>,
    url: String,
) -> Result<String, String> {
    ensure_local(&webview)?;
    let base = normalize_server_url(&url)?;
    let (base, app_name) = check_server(&base).await?;

    let store = app.store(STORE_FILE).map_err(|e| e.to_string())?;
    store.set(KEY_SERVER, base.as_str());
    store.set(KEY_LAST, base.as_str());
    store.save().map_err(|e| e.to_string())?;

    let state = app.state::<AppState>();
    *state.server.lock().unwrap() = Some(base.clone());

    if let Some(window) = app.get_webview_window(MAIN) {
        let _ = window.set_title(&app_name);
        window.navigate(base.clone()).map_err(|e| e.to_string())?;
    }
    Ok(display_url(&base))
}

// ---------------------------------------------------------------------------
// Windows
// ---------------------------------------------------------------------------

fn build_main_window<R: Runtime>(app: &AppHandle<R>) -> tauri::Result<WebviewWindow<R>> {
    let state = app.state::<AppState>();
    let server = state.server.clone();
    let nav_app = app.clone();
    let popup_app = app.clone();
    let downloads = state.downloads.clone();
    let dl_app = app.clone();

    WebviewWindowBuilder::new(app, MAIN, WebviewUrl::App(CONNECT_PAGE.into()))
        .title(APP_NAME)
        .inner_size(1280.0, 820.0)
        .min_inner_size(900.0, 600.0)
        .center()
        .devtools(cfg!(debug_assertions))
        // Let the web app receive dropped files (drag-and-drop uploads).
        .disable_drag_drop_handler()
        .on_navigation(move |url| allow_navigation(&nav_app, &server, url))
        .on_new_window(move |url, _features| handle_new_window(&popup_app, url))
        .on_document_title_changed(|window, title| set_title(&window, &title))
        .on_download(move |_webview, event| handle_download(&dl_app, &downloads, event))
        .build()
}

fn set_title<R: Runtime>(window: &WebviewWindow<R>, title: &str) {
    let title = title.trim();
    let _ = window.set_title(if title.is_empty() { APP_NAME } else { title });
}

/// `window.open` / `target="_blank"`: same-origin pages open in a new app
/// window (sharing the session), everything else in the system browser.
fn handle_new_window<R: Runtime>(app: &AppHandle<R>, url: Url) -> NewWindowResponse<R> {
    let state = app.state::<AppState>();
    if is_server_url(&state.server, &url) {
        let app = app.clone();
        let n = state.viewer_seq.fetch_add(1, Ordering::Relaxed) + 1;
        tauri::async_runtime::spawn(async move {
            if let Err(e) = build_viewer_window(&app, &format!("viewer-{n}"), url) {
                eprintln!("coffer: could not open window: {e}");
            }
        });
    } else {
        open_external(app, &url);
    }
    NewWindowResponse::Deny
}

fn build_viewer_window<R: Runtime>(app: &AppHandle<R>, label: &str, url: Url) -> tauri::Result<()> {
    let state = app.state::<AppState>();
    let server = state.server.clone();
    let nav_app = app.clone();
    let popup_app = app.clone();

    let builder = WebviewWindowBuilder::new(app, label, WebviewUrl::External(url))
        .title(APP_NAME)
        .inner_size(1100.0, 760.0)
        .min_inner_size(480.0, 360.0)
        .devtools(cfg!(debug_assertions))
        .disable_drag_drop_handler()
        .on_navigation(move |url| !is_local(url) && allow_navigation(&nav_app, &server, url))
        .on_new_window(move |url, _features| handle_new_window(&popup_app, url))
        .on_document_title_changed(|window, title| set_title(&window, &title));

    // On Linux the download handler is registered on the shared web context by
    // the main window and already covers every webview; adding it again would
    // fire twice. Elsewhere it is per-webview.
    #[cfg(not(target_os = "linux"))]
    let builder = {
        let downloads = state.downloads.clone();
        let dl_app = app.clone();
        builder.on_download(move |_webview, event| handle_download(&dl_app, &downloads, event))
    };

    builder.build()?;
    Ok(())
}

fn close_viewer_windows<R: Runtime>(app: &AppHandle<R>) {
    for (label, window) in app.webview_windows() {
        if label != MAIN {
            let _ = window.close();
        }
    }
}

/// Forgets the saved server and shows the connect page.
fn change_server<R: Runtime>(app: &AppHandle<R>) {
    if let Ok(store) = app.store(STORE_FILE) {
        store.delete(KEY_SERVER);
        let _ = store.save();
    }
    *app.state::<AppState>().server.lock().unwrap() = None;
    close_viewer_windows(app);
    if let Some(window) = app.get_webview_window(MAIN) {
        let _ = window.set_title(APP_NAME);
        let _ = window.navigate(connect_page_url());
        let _ = window.show();
        let _ = window.set_focus();
    }
}

fn focus_main<R: Runtime>(app: &AppHandle<R>) {
    if let Some(window) = app.get_webview_window(MAIN) {
        let _ = window.unminimize();
        let _ = window.show();
        let _ = window.set_focus();
    }
}

// ---------------------------------------------------------------------------
// Downloads
// ---------------------------------------------------------------------------

fn handle_download<R: Runtime>(
    app: &AppHandle<R>,
    downloads: &Mutex<Downloads>,
    event: DownloadEvent<'_>,
) -> bool {
    match event {
        DownloadEvent::Requested { url, destination } => {
            // Only download from the server (or page-generated blob/data URLs).
            let state = app.state::<AppState>();
            let allowed = match url.scheme() {
                "http" | "https" => is_server_url(&state.server, &url),
                "blob" | "data" => true,
                _ => false,
            };
            if !allowed {
                open_external(app, &url);
                return false;
            }

            let suggested = destination
                .file_name()
                .map(|n| n.to_string_lossy().into_owned())
                .unwrap_or_default();
            let suggested = match destination.parent() {
                Some(parent) => undo_webview_suffix(parent, &suggested),
                None => suggested,
            };
            let name = sanitize_file_name(&suggested);
            let dir = app
                .path()
                .download_dir()
                .ok()
                .or_else(|| destination.parent().map(Path::to_path_buf))
                .unwrap_or_else(std::env::temp_dir);
            let _ = std::fs::create_dir_all(&dir);

            let mut dl = downloads.lock().unwrap();
            let path = unique_path(&dir, &name, &dl.reserved);
            dl.reserved.insert(path.clone());
            dl.pending
                .entry(url.to_string())
                .or_default()
                .push_back(path.clone());
            *destination = path;
            true
        }
        DownloadEvent::Finished { url, path, success } => {
            let ours = {
                let mut dl = downloads.lock().unwrap();
                let key = url.to_string();
                let ours = dl.pending.get_mut(&key).and_then(VecDeque::pop_front);
                if dl.pending.get(&key).is_some_and(VecDeque::is_empty) {
                    dl.pending.remove(&key);
                }
                if let Some(p) = &ours {
                    dl.reserved.remove(p);
                }
                ours
            };
            let path = path
                .map(|p| {
                    // Some platforms report a file:// URI.
                    let s = p.to_string_lossy();
                    match s.strip_prefix("file://") {
                        Some(rest) => PathBuf::from(rest),
                        None => p,
                    }
                })
                .or(ours);
            let name = path
                .as_deref()
                .and_then(Path::file_name)
                .map(|n| n.to_string_lossy().into_owned())
                .unwrap_or_else(|| "File".into());
            let (title, body) = if success {
                (
                    "Download complete",
                    format!("{name} was saved to Downloads."),
                )
            } else {
                (
                    "Download failed",
                    format!("{name} could not be downloaded."),
                )
            };
            if let Err(e) = app.notification().builder().title(title).body(body).show() {
                eprintln!("coffer: notification failed: {e}");
            }
            true
        }
        _ => true,
    }
}

/// Keeps a file name safe to create inside the Downloads folder.
fn sanitize_file_name(name: &str) -> String {
    let cleaned: String = name
        .chars()
        .map(|c| match c {
            '/' | '\\' | ':' | '*' | '?' | '"' | '<' | '>' | '|' => '_',
            c if c.is_control() => '_',
            c => c,
        })
        .collect();
    let cleaned = cleaned.trim().trim_start_matches('.').trim();
    if cleaned.is_empty() {
        "download".into()
    } else {
        cleaned.chars().take(200).collect()
    }
}

/// The webview hands over a destination it already made unique, as
/// `<base> (n)<ext>` with the extension split at the *first* dot
/// (`report (1).final.pdf`). Recover the original name so `unique_path` can
/// number it properly (`report.final (1).pdf`).
fn undo_webview_suffix(dir: &Path, name: &str) -> String {
    let first_dot = name.find('.').unwrap_or(name.len());
    let (head, ext) = name.split_at(first_dot);
    if let Some(base) = head.strip_suffix(')') {
        if let Some(open) = base.rfind(" (") {
            let digits = &base[open + 2..];
            if !digits.is_empty() && digits.bytes().all(|b| b.is_ascii_digit()) {
                let original = format!("{}{ext}", &base[..open]);
                if dir.join(&original).exists() {
                    return original;
                }
            }
        }
    }
    name.to_string()
}

/// `name.ext`, then `name (1).ext`, `name (2).ext`, ... until unused.
fn unique_path(dir: &Path, name: &str, reserved: &HashSet<PathBuf>) -> PathBuf {
    let (stem, ext) = split_extension(name);
    let mut candidate = dir.join(name);
    let mut n = 1;
    while candidate.exists() || reserved.contains(&candidate) {
        candidate = dir.join(format!("{stem} ({n}){ext}"));
        n += 1;
    }
    candidate
}

fn split_extension(name: &str) -> (&str, &str) {
    for double in [".tar.gz", ".tar.bz2", ".tar.xz", ".tar.zst"] {
        if name.len() > double.len() && name.to_ascii_lowercase().ends_with(double) {
            let i = name.len() - double.len();
            return (&name[..i], &name[i..]);
        }
    }
    match name.rfind('.') {
        Some(i) if i > 0 => (&name[..i], &name[i..]),
        _ => (name, ""),
    }
}

// ---------------------------------------------------------------------------
// Menu
// ---------------------------------------------------------------------------

fn build_menu<R: Runtime>(app: &AppHandle<R>) -> tauri::Result<Menu<R>> {
    let pkg = app.package_info();
    let about = AboutMetadata {
        name: Some(APP_NAME.into()),
        version: Some(pkg.version.to_string()),
        copyright: Some("Copyright © 2026 Odd Omens LLC".into()),
        license: Some("AGPL-3.0-only".into()),
        comments: Some("Desktop app for Coffer, the self-hosted file browser.".into()),
        icon: app.default_window_icon().cloned(),
        ..Default::default()
    };

    let change_server = MenuItem::with_id(
        app,
        MENU_CHANGE_SERVER,
        "Change Server…",
        true,
        None::<&str>,
    )?;
    let reload = MenuItem::with_id(app, MENU_RELOAD, "Reload", true, Some("CmdOrCtrl+R"))?;
    let sep = || PredefinedMenuItem::separator(app);

    #[cfg(target_os = "macos")]
    let app_menu = Submenu::with_items(
        app,
        APP_NAME,
        true,
        &[
            &PredefinedMenuItem::about(app, Some("About Coffer"), Some(about))?,
            &sep()?,
            &change_server,
            &reload,
            &sep()?,
            &PredefinedMenuItem::services(app, None)?,
            &sep()?,
            &PredefinedMenuItem::hide(app, None)?,
            &PredefinedMenuItem::hide_others(app, None)?,
            &PredefinedMenuItem::show_all(app, None)?,
            &sep()?,
            &PredefinedMenuItem::quit(app, Some("Quit Coffer"))?,
        ],
    )?;
    #[cfg(not(target_os = "macos"))]
    let app_menu = Submenu::with_items(
        app,
        APP_NAME,
        true,
        &[
            &PredefinedMenuItem::about(app, Some("About Coffer"), Some(about))?,
            &sep()?,
            &change_server,
            &reload,
            &sep()?,
            &MenuItem::with_id(app, MENU_QUIT, "Quit", true, Some("CmdOrCtrl+Q"))?,
        ],
    )?;

    let edit_menu = Submenu::with_items(
        app,
        "Edit",
        true,
        &[
            &PredefinedMenuItem::undo(app, None)?,
            &PredefinedMenuItem::redo(app, None)?,
            &sep()?,
            &PredefinedMenuItem::cut(app, None)?,
            &PredefinedMenuItem::copy(app, None)?,
            &PredefinedMenuItem::paste(app, None)?,
            &PredefinedMenuItem::select_all(app, None)?,
        ],
    )?;

    let view_menu = Submenu::with_items(
        app,
        "View",
        true,
        &[
            &MenuItem::with_id(app, MENU_ZOOM_IN, "Zoom In", true, Some("CmdOrCtrl+="))?,
            &MenuItem::with_id(app, MENU_ZOOM_OUT, "Zoom Out", true, Some("CmdOrCtrl+-"))?,
            &MenuItem::with_id(
                app,
                MENU_ZOOM_RESET,
                "Actual Size",
                true,
                Some("CmdOrCtrl+0"),
            )?,
            #[cfg(target_os = "macos")]
            &sep()?,
            #[cfg(target_os = "macos")]
            &PredefinedMenuItem::fullscreen(app, None)?,
        ],
    )?;

    #[cfg(target_os = "macos")]
    {
        let window_menu = Submenu::with_id_and_items(
            app,
            WINDOW_SUBMENU_ID,
            "Window",
            true,
            &[
                &PredefinedMenuItem::minimize(app, None)?,
                &PredefinedMenuItem::maximize(app, Some("Zoom"))?,
                &sep()?,
                &PredefinedMenuItem::close_window(app, None)?,
            ],
        )?;
        let help_menu = Submenu::with_id_and_items(app, HELP_SUBMENU_ID, "Help", true, &[])?;
        Menu::with_items(
            app,
            &[&app_menu, &edit_menu, &view_menu, &window_menu, &help_menu],
        )
    }
    #[cfg(not(target_os = "macos"))]
    Menu::with_items(app, &[&app_menu, &edit_menu, &view_menu])
}

/// The window a menu command applies to: the focused one, else the main one.
fn target_window<R: Runtime>(app: &AppHandle<R>) -> Option<WebviewWindow<R>> {
    app.webview_windows()
        .into_values()
        .find(|w| w.is_focused().unwrap_or(false))
        .or_else(|| app.get_webview_window(MAIN))
}

fn zoom<R: Runtime>(app: &AppHandle<R>, direction: i32) {
    let Some(window) = target_window(app) else {
        return;
    };
    let state = app.state::<AppState>();
    let mut levels = state.zoom.lock().unwrap();
    let current = *levels.get(window.label()).unwrap_or(&1.0);
    let next = match direction {
        0 => 1.0,
        d if d > 0 => ZOOM_STEPS
            .iter()
            .copied()
            .find(|z| *z > current + 0.001)
            .unwrap_or(current),
        _ => ZOOM_STEPS
            .iter()
            .rev()
            .copied()
            .find(|z| *z < current - 0.001)
            .unwrap_or(current),
    };
    if window.set_zoom(next).is_ok() {
        levels.insert(window.label().to_string(), next);
    }
}

fn on_menu_event<R: Runtime>(app: &AppHandle<R>, event: MenuEvent) {
    match event.id().as_ref() {
        MENU_CHANGE_SERVER => change_server(app),
        MENU_RELOAD => {
            if let Some(window) = target_window(app) {
                let _ = window.reload();
            }
        }
        MENU_QUIT => app.exit(0),
        MENU_ZOOM_IN => zoom(app, 1),
        MENU_ZOOM_OUT => zoom(app, -1),
        MENU_ZOOM_RESET => zoom(app, 0),
        _ => {}
    }
}

// ---------------------------------------------------------------------------
// Entry point
// ---------------------------------------------------------------------------

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        // Must be first: a second launch just focuses the running window.
        .plugin(tauri_plugin_single_instance::init(|app, _argv, _cwd| {
            focus_main(app)
        }))
        .plugin(
            tauri_plugin_window_state::Builder::default()
                .with_filter(|label| label == MAIN)
                .build(),
        )
        .plugin(tauri_plugin_store::Builder::default().build())
        // Rust-side use only. Its injected click handler for target="_blank"
        // links would call IPC from the remote page (which is denied), so it
        // is off; `handle_new_window` deals with those links natively.
        .plugin(
            tauri_plugin_opener::Builder::new()
                .open_js_links_on_click(false)
                .build(),
        )
        .plugin(tauri_plugin_notification::init())
        .manage(AppState::default())
        .menu(build_menu)
        .on_menu_event(on_menu_event)
        .invoke_handler(tauri::generate_handler![startup, connect])
        .setup(|app| {
            build_main_window(app.handle())?;
            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("error while running Coffer");
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn normalizes_urls() {
        let n = |s: &str| normalize_server_url(s).map(|u| u.to_string());
        assert_eq!(n("192.168.1.20:8080").unwrap(), "http://192.168.1.20:8080/");
        assert_eq!(
            n("files.example.com").unwrap(),
            "https://files.example.com/"
        );
        assert_eq!(n("nas").unwrap(), "http://nas/");
        assert_eq!(
            n(" https://files.example.com/coffer/?x=1#y ").unwrap(),
            "https://files.example.com/coffer/"
        );
        assert_eq!(
            n("http://localhost:8080//").unwrap(),
            "http://localhost:8080/"
        );
        assert!(n("ftp://example.com").is_err());
        assert!(n("").is_err());
        assert!(n("https://user:pw@example.com").is_err());
    }

    #[test]
    fn navigation_origins() {
        let server = Url::parse("http://192.168.1.20:8080/").unwrap();
        assert!(same_origin(
            &server,
            &Url::parse("http://192.168.1.20:8080/files/a").unwrap()
        ));
        assert!(!same_origin(
            &server,
            &Url::parse("http://192.168.1.20:8081/s/x").unwrap()
        ));
        assert!(!same_origin(
            &server,
            &Url::parse("https://192.168.1.20:8080/").unwrap()
        ));
        assert!(is_local(&connect_page_url()));
        assert!(!is_local(&server));
    }

    #[test]
    fn unique_names() {
        let dir = std::env::temp_dir().join("coffer-desktop-test-unique");
        let _ = std::fs::remove_dir_all(&dir);
        std::fs::create_dir_all(&dir).unwrap();
        std::fs::write(dir.join("clip.mov"), b"x").unwrap();
        let mut reserved = HashSet::new();
        let p = unique_path(&dir, "clip.mov", &reserved);
        assert_eq!(p, dir.join("clip (1).mov"));
        reserved.insert(p);
        assert_eq!(
            unique_path(&dir, "clip.mov", &reserved),
            dir.join("clip (2).mov")
        );
        assert_eq!(
            unique_path(&dir, "a.tar.gz", &reserved),
            dir.join("a.tar.gz")
        );
        assert_eq!(split_extension("a.tar.gz"), ("a", ".tar.gz"));
        assert_eq!(split_extension(".env"), (".env", ""));
        assert_eq!(sanitize_file_name("../x/y?.txt"), "_x_y_.txt");
        std::fs::write(dir.join("report.final.pdf"), b"x").unwrap();
        assert_eq!(
            undo_webview_suffix(&dir, "report (1).final.pdf"),
            "report.final.pdf"
        );
        assert_eq!(undo_webview_suffix(&dir, "notes (3).txt"), "notes (3).txt");
        let r = HashSet::new();
        assert_eq!(
            unique_path(&dir, "report.final.pdf", &r),
            dir.join("report.final (1).pdf")
        );
        let _ = std::fs::remove_dir_all(&dir);
    }
}
