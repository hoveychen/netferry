import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { useTranslation } from "react-i18next";
import { QRCodeSVG } from "qrcode.react";
import { ChevronLeft, ChevronRight, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import type { Profile } from "@/types";
import { exportProfile } from "@/api";

/** Max bytes of base64 payload per QR code (keeps QR at ~version 20, easy to scan). */
const CHUNK_SIZE = 1000;

function splitIntoChunks(data: string): string[] {
  const total = Math.ceil(data.length / CHUNK_SIZE);
  const chunks: string[] = [];
  for (let i = 0; i < total; i++) {
    const slice = data.slice(i * CHUNK_SIZE, (i + 1) * CHUNK_SIZE);
    chunks.push(`NF:${i + 1}/${total}:${slice}`);
  }
  return chunks;
}

interface Props {
  profile: Profile;
  onClose: () => void;
}

export function QrCodeExportDialog({ profile, onClose }: Props) {
  const { t } = useTranslation();
  const [chunks, setChunks] = useState<string[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [page, setPage] = useState(0);

  useEffect(() => {
    exportProfile(profile)
      .then((encrypted) => setChunks(splitIntoChunks(encrypted)))
      .catch((err) => setError(String(err)));
  }, [profile]);

  const total = chunks?.length ?? 0;

  return createPortal(
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm" onClick={onClose}>
      <div className="relative w-full max-w-sm rounded-2xl border border-bdr bg-elevated p-6 shadow-2xl" onClick={(e) => e.stopPropagation()}>
        {/* Close button */}
        <button
          type="button"
          className="absolute right-4 top-4 rounded-lg p-1 text-t4 hover:bg-ov-8 hover:text-t2"
          onClick={onClose}
        >
          <X className="h-4 w-4" />
        </button>

        <h2 className="mb-1 pr-8 text-lg font-semibold text-t1">{t("qrExport.title")}</h2>
        <p className="mb-5 text-xs text-t3">{profile.name}</p>

        {error && (
          <p className="rounded-lg border border-danger/20 bg-danger/[0.08] px-3 py-2 text-sm text-danger">
            {error}
          </p>
        )}

        {!chunks && !error && (
          <div className="flex h-64 items-center justify-center text-sm text-t3">
            {t("qrExport.generating")}
          </div>
        )}

        {chunks && (
          <>
            {/* QR code with visible label */}
            <div className="flex flex-col items-center rounded-xl bg-white p-4">
              {total > 1 && (
                <p className="mb-2 text-sm font-semibold text-gray-700">
                  {t("qrExport.qrLabel", { current: page + 1, total, name: profile.name })}
                </p>
              )}
              <QRCodeSVG value={chunks[page]} size={256} level="M" />
              {total > 1 && (
                <p className="mt-2 text-[11px] text-gray-400">
                  {t("qrExport.qrScanOrder", { current: page + 1, total })}
                </p>
              )}
            </div>

            {/* Page indicator & navigation */}
            {total > 1 && (
              <div className="mt-4 flex items-center justify-center gap-3">
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={page === 0}
                  onClick={() => setPage((p) => p - 1)}
                >
                  <ChevronLeft className="h-4 w-4" />
                </Button>
                <span className="min-w-[4rem] text-center text-sm text-t2">
                  {t("qrExport.pageIndicator", { current: page + 1, total })}
                </span>
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={page === total - 1}
                  onClick={() => setPage((p) => p + 1)}
                >
                  <ChevronRight className="h-4 w-4" />
                </Button>
              </div>
            )}

            {total > 1 && (
              <p className="mt-2 text-center text-[11px] text-t4">
                {t("qrExport.multiHint")}
              </p>
            )}
          </>
        )}
      </div>
    </div>,
    document.body,
  );
}
