import { useTranslation } from "react-i18next";
import { FileText, Import } from "lucide-react";
import { Button } from "@/components/ui/button";

interface Props {
  open: boolean;
  onClose: () => void;
  onBlank: () => void;
  onImportSsh: () => void;
}

export function NewProfileDialog({ open, onClose, onBlank, onImportSsh }: Props) {
  const { t } = useTranslation();

  if (!open) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/55 p-4 backdrop-blur-sm">
      <div className="w-full max-w-sm rounded-2xl border border-bdr bg-elevated p-6 shadow-2xl shadow-black/60">
        <h3 className="mb-1 text-[17px] font-semibold text-t1">{t("newProfile.title")}</h3>
        <p className="mb-5 text-sm text-t3">{t("newProfile.subtitle")}</p>

        <div className="flex flex-col gap-2.5">
          <button
            type="button"
            className="flex items-center gap-4 rounded-xl border border-sep bg-ov-4 p-4 text-left transition-all hover:border-edge hover:bg-ov-8"
            onClick={() => {
              onClose();
              onBlank();
            }}
          >
            <div className="flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-xl bg-ov-8">
              <FileText className="h-5 w-5 text-t2" />
            </div>
            <div>
              <p className="text-sm font-semibold text-t1">{t("newProfile.blank")}</p>
              <p className="text-xs text-t3">{t("newProfile.blankDesc")}</p>
            </div>
          </button>

          <button
            type="button"
            className="flex items-center gap-4 rounded-xl border border-sep bg-ov-4 p-4 text-left transition-all hover:border-edge hover:bg-ov-8"
            onClick={() => {
              onClose();
              onImportSsh();
            }}
          >
            <div className="flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-xl bg-ov-8">
              <Import className="h-5 w-5 text-t2" />
            </div>
            <div>
              <p className="text-sm font-semibold text-t1">{t("newProfile.importSsh")}</p>
              <p className="text-xs text-t3">{t("newProfile.importSshDesc")}</p>
            </div>
          </button>
        </div>

        <div className="mt-5 flex justify-end">
          <Button variant="secondary" size="sm" onClick={onClose}>
            {t("nav.cancel")}
          </Button>
        </div>
      </div>
    </div>
  );
}
