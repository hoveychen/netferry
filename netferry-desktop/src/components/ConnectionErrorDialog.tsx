import { createPortal } from "react-dom";
import { useTranslation } from "react-i18next";
import { AlertTriangle } from "lucide-react";
import { Button } from "@/components/ui/button";
import type { TunnelError } from "@/types";

interface Props {
  open: boolean;
  message?: string;
  errors: TunnelError[];
  profileName?: string;
  onClose: () => void;
}

export function ConnectionErrorDialog({ open, message, errors, profileName, onClose }: Props) {
  const { t } = useTranslation();

  if (!open) return null;

  return createPortal(
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm"
      onClick={onClose}
    >
      <div
        className="flex max-h-[80vh] w-full max-w-xl flex-col rounded-2xl border border-bdr bg-elevated p-6 shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="mb-3 flex items-center gap-2">
          <AlertTriangle className="h-5 w-5 text-danger" />
          <h2 className="text-lg font-semibold text-t1">
            {t("connectionError.title")}
          </h2>
        </div>

        {profileName && (
          <p className="mb-3 text-sm text-t3">
            {t("connectionError.profileLabel", { name: profileName })}
          </p>
        )}

        {message && (
          <p className="mb-3 rounded-lg border border-danger/20 bg-danger/[0.08] px-3 py-2 text-sm text-danger">
            {message}
          </p>
        )}

        <div className="mb-4 min-h-0 flex-1 overflow-hidden">
          <div className="mb-2 text-xs font-medium uppercase tracking-wide text-t3">
            {t("connectionError.detailsTitle")}
          </div>
          {errors.length === 0 ? (
            <p className="rounded-lg border border-bdr bg-ov-4 px-3 py-3 text-sm text-t3">
              {t("connectionError.noDetails")}
            </p>
          ) : (
            <div className="max-h-[40vh] overflow-auto rounded-lg border border-bdr bg-ov-4 p-3">
              <ul className="space-y-2 font-mono text-xs text-t2">
                {errors.map((e, i) => (
                  <li key={`${e.timestampMs}-${i}`} className="break-words">
                    <span className="mr-2 text-t4">
                      {new Date(e.timestampMs).toLocaleTimeString()}
                    </span>
                    <span className="text-danger">{e.message}</span>
                  </li>
                ))}
              </ul>
            </div>
          )}
        </div>

        <div className="flex justify-end gap-2">
          <Button size="sm" onClick={onClose}>
            {t("connectionError.dismiss")}
          </Button>
        </div>
      </div>
    </div>,
    document.body,
  );
}
