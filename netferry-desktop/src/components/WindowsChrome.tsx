import { useEffect, useState } from "react";
import chromeStyles from "@/windows-chrome.module.css";

// Win11 caption-button glyphs. Inline 10×10 viewBox so the rendered stroke
// stays at 1px (Rule 6a) — pushing them through a shared 16×16 Icon would
// render ~5px glyphs with 0.6px strokes.
const CaptionMinimize = () => (
  <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" strokeWidth="1" aria-hidden>
    <path d="M0 5h10" />
  </svg>
);
const CaptionMaximize = () => (
  <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" strokeWidth="1" aria-hidden>
    <path d="M0.5 0.5h9v9h-9z" />
  </svg>
);
const CaptionRestore = () => (
  <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" strokeWidth="1" aria-hidden>
    <path d="M2.5 2.5h7v7h-7z M2.5 2.5V0.5h7v7h-2" />
  </svg>
);
const CaptionClose = () => (
  <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" strokeWidth="1" strokeLinecap="square" aria-hidden>
    <path d="M0.5 0.5l9 9 M9.5 0.5l-9 9" />
  </svg>
);

type ResizeDirection =
  | "East"
  | "North"
  | "NorthEast"
  | "NorthWest"
  | "South"
  | "SouthEast"
  | "SouthWest"
  | "West";

async function startResize(direction: ResizeDirection) {
  try {
    const { getCurrentWindow } = await import("@tauri-apps/api/window");
    await getCurrentWindow().startResizeDragging(direction);
  } catch {
    /* no-op outside Tauri */
  }
}

async function captionMinimize() {
  try {
    const { getCurrentWindow } = await import("@tauri-apps/api/window");
    await getCurrentWindow().minimize();
  } catch {
    /* no-op */
  }
}

async function captionToggleMaximize() {
  try {
    const { getCurrentWindow } = await import("@tauri-apps/api/window");
    await getCurrentWindow().toggleMaximize();
  } catch {
    /* no-op */
  }
}

// Close hides to tray (mirrors menu.rs::close_window). The tunnel keeps
// running; tray "Quit" is the only real exit on Windows.
async function captionClose() {
  try {
    const { getCurrentWindow } = await import("@tauri-apps/api/window");
    await getCurrentWindow().hide();
  } catch {
    /* no-op */
  }
}

export function WindowsChrome() {
  const [maximized, setMaximized] = useState(false);

  useEffect(() => {
    let unlisten: (() => void) | undefined;
    let cancelled = false;
    void (async () => {
      try {
        const { getCurrentWindow } = await import("@tauri-apps/api/window");
        const w = getCurrentWindow();
        const seed = await w.isMaximized();
        if (!cancelled) setMaximized(seed);
        unlisten = await w.onResized(async () => {
          try {
            const m = await w.isMaximized();
            if (!cancelled) setMaximized(m);
          } catch {
            /* ignore */
          }
        });
      } catch {
        /* not a tauri host */
      }
    })();
    return () => {
      cancelled = true;
      unlisten?.();
    };
  }, []);

  return (
    <>
      {/* Caption buttons: top-right of the drag strip. CSS gates visibility
          on html.tauri-host.os-windows so macOS keeps native traffic lights. */}
      <div className={chromeStyles.captionGroup}>
        <button
          type="button"
          className={chromeStyles.captionBtn}
          onClick={captionMinimize}
          aria-label="Minimize"
          title="Minimize"
        >
          <CaptionMinimize />
        </button>
        <button
          type="button"
          className={chromeStyles.captionBtn}
          onClick={captionToggleMaximize}
          aria-label={maximized ? "Restore" : "Maximize"}
          title={maximized ? "Restore" : "Maximize"}
        >
          {maximized ? <CaptionRestore /> : <CaptionMaximize />}
        </button>
        <button
          type="button"
          className={`${chromeStyles.captionBtn} ${chromeStyles.captionClose}`}
          onClick={captionClose}
          aria-label="Close"
          title="Close"
        >
          <CaptionClose />
        </button>
      </div>

      {/* Resize handles. mousedown hands the loop off to Tauri's
          startResizeDragging so cursor + behavior are native. */}
      <div className={`${chromeStyles.resize} ${chromeStyles.resizeN}`} onMouseDown={(e) => { e.preventDefault(); void startResize("North"); }} />
      <div className={`${chromeStyles.resize} ${chromeStyles.resizeS}`} onMouseDown={(e) => { e.preventDefault(); void startResize("South"); }} />
      <div className={`${chromeStyles.resize} ${chromeStyles.resizeW}`} onMouseDown={(e) => { e.preventDefault(); void startResize("West"); }} />
      <div className={`${chromeStyles.resize} ${chromeStyles.resizeE}`} onMouseDown={(e) => { e.preventDefault(); void startResize("East"); }} />
      <div className={`${chromeStyles.resize} ${chromeStyles.resizeNW}`} onMouseDown={(e) => { e.preventDefault(); void startResize("NorthWest"); }} />
      <div className={`${chromeStyles.resize} ${chromeStyles.resizeNE}`} onMouseDown={(e) => { e.preventDefault(); void startResize("NorthEast"); }} />
      <div className={`${chromeStyles.resize} ${chromeStyles.resizeSW}`} onMouseDown={(e) => { e.preventDefault(); void startResize("SouthWest"); }} />
      <div className={`${chromeStyles.resize} ${chromeStyles.resizeSE}`} onMouseDown={(e) => { e.preventDefault(); void startResize("SouthEast"); }} />
    </>
  );
}
