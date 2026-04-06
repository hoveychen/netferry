import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { ArrowUpCircle, X } from "lucide-react";
import { openUrl } from "@tauri-apps/plugin-opener";
import { checkForUpdate } from "@/api";
import type { UpdateInfo } from "@/types";

const CHECK_INTERVAL_MS = 24 * 60 * 60 * 1000; // 24 hours
const LAST_CHECK_KEY = "netferry_update_last_check";
const DISMISSED_VERSION_KEY = "netferry_update_dismissed";

export function UpdateBanner() {
  const { t } = useTranslation();
  const [update, setUpdate] = useState<UpdateInfo | null>(null);

  useEffect(() => {
    const doCheck = async () => {
      try {
        const info = await checkForUpdate();
        if (info.has_update) {
          const dismissed = localStorage.getItem(DISMISSED_VERSION_KEY);
          if (dismissed !== info.latest_version) {
            setUpdate(info);
          }
        }
        localStorage.setItem(LAST_CHECK_KEY, Date.now().toString());
      } catch (e) {
        console.warn("Update check failed:", e);
      }
    };

    // Check on mount if enough time has passed (or first time)
    const lastCheck = parseInt(localStorage.getItem(LAST_CHECK_KEY) ?? "0", 10);
    const elapsed = Date.now() - lastCheck;
    if (elapsed >= CHECK_INTERVAL_MS || lastCheck === 0) {
      doCheck();
    }

    // Set up periodic check
    const timer = setInterval(doCheck, CHECK_INTERVAL_MS);

    // Listen for force-check from menu bar "Check for Updates…"
    const forceCheck = () => doCheck();
    window.addEventListener("force-update-check", forceCheck);

    return () => {
      clearInterval(timer);
      window.removeEventListener("force-update-check", forceCheck);
    };
  }, []);

  if (!update) return null;

  const handleDownload = () => {
    openUrl(update.release_url);
  };

  const handleDismiss = () => {
    localStorage.setItem(DISMISSED_VERSION_KEY, update.latest_version);
    setUpdate(null);
  };

  return (
    <div className="flex items-center gap-3 border-b border-sep bg-accent/8 px-4 py-2.5">
      <ArrowUpCircle size={16} className="shrink-0 text-accent" />
      <span className="min-w-0 flex-1 text-[13px] text-t1">
        {t("update.available", { version: update.latest_version })}
      </span>
      <button
        onClick={handleDownload}
        className="shrink-0 rounded-lg bg-accent px-3 py-1 text-[12px] font-medium text-white transition-opacity hover:opacity-90"
      >
        {t("update.download")}
      </button>
      <button
        onClick={handleDismiss}
        className="shrink-0 rounded-md p-0.5 text-t3 transition-colors hover:bg-ov-10 hover:text-t1"
        title={t("update.dismiss")}
      >
        <X size={14} />
      </button>
    </div>
  );
}
