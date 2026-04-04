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
      <div className="w-full max-w-sm rounded-2xl border border-white/[0.10] bg-[#2c2c2e] p-6 shadow-2xl shadow-black/60">
        <h3 className="mb-1 text-[17px] font-semibold text-white/90">{t("newProfile.title")}</h3>
        <p className="mb-5 text-sm text-white/45">{t("newProfile.subtitle")}</p>

        <div className="flex flex-col gap-2.5">
          <button
            type="button"
            className="flex items-center gap-4 rounded-xl border border-white/[0.07] bg-white/[0.04] p-4 text-left transition-all hover:border-white/[0.13] hover:bg-white/[0.08]"
            onClick={() => {
              onClose();
              onBlank();
            }}
          >
            <div className="flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-xl bg-white/[0.08]">
              <FileText className="h-5 w-5 text-white/55" />
            </div>
            <div>
              <p className="text-sm font-semibold text-white/85">{t("newProfile.blank")}</p>
              <p className="text-xs text-white/40">{t("newProfile.blankDesc")}</p>
            </div>
          </button>

          <button
            type="button"
            className="flex items-center gap-4 rounded-xl border border-white/[0.07] bg-white/[0.04] p-4 text-left transition-all hover:border-white/[0.13] hover:bg-white/[0.08]"
            onClick={() => {
              onClose();
              onImportSsh();
            }}
          >
            <div className="flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-xl bg-white/[0.08]">
              <Import className="h-5 w-5 text-white/55" />
            </div>
            <div>
              <p className="text-sm font-semibold text-white/85">{t("newProfile.importSsh")}</p>
              <p className="text-xs text-white/40">{t("newProfile.importSshDesc")}</p>
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
