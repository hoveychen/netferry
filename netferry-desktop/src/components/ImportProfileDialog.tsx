import { useState } from "react";
import { createPortal } from "react-dom";
import { useTranslation } from "react-i18next";
import { Download } from "lucide-react";
import { open as showOpenDialog } from "@tauri-apps/plugin-dialog";
import { Button } from "@/components/ui/button";

interface Props {
  open: boolean;
  onClose: () => void;
  onImport: (data: string) => Promise<void>;
  onImportFile: (path: string) => Promise<void>;
}

export function ImportProfileDialog({ open, onClose, onImport, onImportFile }: Props) {
  const { t } = useTranslation();
  const [text, setText] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [importing, setImporting] = useState(false);
  const [dragging, setDragging] = useState(false);

  if (!open) return null;

  const close = () => {
    setText("");
    setError(null);
    onClose();
  };

  const handleOpenFile = async () => {
    setError(null);
    try {
      const path = await showOpenDialog({
        title: t("importDialog.title"),
        filters: [{ name: "NetFerry Profile", extensions: ["nfprofile"] }],
        multiple: false,
        directory: false,
      });
      if (!path) return;
      setImporting(true);
      await onImportFile(path);
      close();
    } catch (err) {
      setError(String(err));
    } finally {
      setImporting(false);
    }
  };

  const handleImportText = async () => {
    if (!text.trim()) return;
    setImporting(true);
    setError(null);
    try {
      await onImport(text.trim());
      close();
    } catch (err) {
      setError(String(err));
    } finally {
      setImporting(false);
    }
  };

  const handleFileDrop = async (e: React.DragEvent) => {
    e.preventDefault();
    setDragging(false);
    const file = e.dataTransfer.files[0];
    if (!file) return;
    setImporting(true);
    setError(null);
    try {
      const raw = await file.text();
      await onImport(raw.trim());
      close();
    } catch (err) {
      setError(String(err));
    } finally {
      setImporting(false);
    }
  };

  return createPortal(
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm"
      onClick={close}
    >
      <div
        className="w-full max-w-lg rounded-2xl border border-bdr bg-elevated p-6 shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      >
        <h2 className="mb-4 text-lg font-semibold text-t1">{t("importDialog.title")}</h2>

        <button
          type="button"
          className={`mb-4 flex w-full items-center justify-center gap-2 rounded-xl border border-dashed px-4 py-4 text-sm transition-colors ${
            dragging
              ? "border-accent bg-accent/10 text-accent"
              : "border-edge bg-ov-3 text-t3 hover:border-accent/40 hover:bg-accent/[0.05] hover:text-t2"
          }`}
          onClick={handleOpenFile}
          disabled={importing}
          onDragOver={(e) => { e.preventDefault(); setDragging(true); }}
          onDragEnter={(e) => { e.preventDefault(); setDragging(true); }}
          onDragLeave={() => setDragging(false)}
          onDrop={handleFileDrop}
        >
          <Download className="h-4 w-4" />
          {dragging ? t("importDialog.dropFile") : t("importDialog.openFile")}
        </button>

        <div className="mb-3 flex items-center gap-3">
          <div className="h-px flex-1 bg-ov-8" />
          <span className="text-[11px] text-t4">{t("importDialog.orPasteText")}</span>
          <div className="h-px flex-1 bg-ov-8" />
        </div>

        <textarea
          className="mb-3 w-full rounded-xl border border-bdr bg-ov-4 px-4 py-3 font-mono text-xs text-t1 placeholder-t4 focus:border-accent/50 focus:outline-none"
          rows={5}
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder={t("importDialog.placeholder")}
        />
        {error && (
          <p className="mb-3 rounded-lg border border-danger/20 bg-danger/[0.08] px-3 py-2 text-sm text-danger">
            {error}
          </p>
        )}
        <div className="flex justify-end gap-2">
          <Button variant="ghost" size="sm" onClick={close}>
            {t("nav.cancel")}
          </Button>
          <Button size="sm" onClick={handleImportText} disabled={importing || !text.trim()}>
            {importing ? t("importDialog.importing") : t("nav.import")}
          </Button>
        </div>
      </div>
    </div>,
    document.body,
  );
}
