import React from "react";
import ReactDOM from "react-dom/client";
import App from "./App";
import "./i18n";
import "./index.css";
import { initTheme } from "@/lib/theme";

initTheme();

// Tag <html> for Tauri-only CSS (drag region, resize handles, caption buttons)
// and per-OS variants. macOS keeps traffic lights via titleBarStyle:"Overlay";
// Windows runs fully frameless and re-implements chrome in WindowsChrome.tsx.
if (typeof window !== "undefined" &&
    ("__TAURI_INTERNALS__" in window || "__TAURI__" in window)) {
  document.documentElement.classList.add("tauri-host");
}
if (typeof navigator !== "undefined") {
  const cls = document.documentElement.classList;
  if (/Windows/i.test(navigator.userAgent)) cls.add("os-windows");
  else if (/Macintosh|Mac OS X/i.test(navigator.userAgent)) cls.add("os-macos");
}


ReactDOM.createRoot(document.getElementById("root") as HTMLElement).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
