import { invoke } from "@tauri-apps/api/core";

export type ThemeMode = "system" | "light" | "dark";

const STORAGE_KEY = "netferry_theme";

function getSystemTheme(): "light" | "dark" {
  return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}

function applyTheme(mode: ThemeMode) {
  const resolved = mode === "system" ? getSystemTheme() : mode;
  document.documentElement.setAttribute("data-theme", resolved);
  // Sync the native macOS window appearance so vibrancy matches
  invoke("set_window_theme", { theme: mode }).catch(() => {});
}

/** Read the stored preference (defaults to "system"). */
export function getThemeMode(): ThemeMode {
  return (localStorage.getItem(STORAGE_KEY) as ThemeMode) || "system";
}

/** Persist and apply a theme preference. */
export function setThemeMode(mode: ThemeMode) {
  localStorage.setItem(STORAGE_KEY, mode);
  applyTheme(mode);
}

/** Call once at startup — applies the stored preference and
 *  listens for OS-level changes when in "system" mode. */
export function initTheme() {
  applyTheme(getThemeMode());

  const mq = window.matchMedia("(prefers-color-scheme: dark)");
  mq.addEventListener("change", () => {
    if (getThemeMode() === "system") {
      applyTheme("system");
    }
  });
}
