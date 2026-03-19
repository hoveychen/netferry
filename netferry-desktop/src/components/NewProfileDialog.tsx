import { FileText, Import } from "lucide-react";
import { Button } from "@/components/ui/button";

interface Props {
  open: boolean;
  onClose: () => void;
  onBlank: () => void;
  onImportSsh: () => void;
}

export function NewProfileDialog({ open, onClose, onBlank, onImportSsh }: Props) {
  if (!open) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4">
      <div className="w-full max-w-sm rounded-xl border border-slate-200 bg-white p-6 shadow-lg">
        <h3 className="mb-1 text-lg font-semibold text-slate-800">Create Profile</h3>
        <p className="mb-5 text-sm text-slate-500">How would you like to get started?</p>

        <div className="flex flex-col gap-3">
          <button
            type="button"
            className="flex items-center gap-4 rounded-lg border border-slate-200 p-4 text-left transition-colors hover:border-slate-400 hover:bg-slate-50"
            onClick={() => {
              onClose();
              onBlank();
            }}
          >
            <div className="flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-full bg-slate-100">
              <FileText className="h-5 w-5 text-slate-600" />
            </div>
            <div>
              <p className="font-medium text-slate-800">Blank Profile</p>
              <p className="text-sm text-slate-500">Start from scratch with default settings.</p>
            </div>
          </button>

          <button
            type="button"
            className="flex items-center gap-4 rounded-lg border border-slate-200 p-4 text-left transition-colors hover:border-slate-400 hover:bg-slate-50"
            onClick={() => {
              onClose();
              onImportSsh();
            }}
          >
            <div className="flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-full bg-slate-100">
              <Import className="h-5 w-5 text-slate-600" />
            </div>
            <div>
              <p className="font-medium text-slate-800">Import from SSH Config</p>
              <p className="text-sm text-slate-500">
                Pre-fill from a host in ~/.ssh/config.
              </p>
            </div>
          </button>
        </div>

        <div className="mt-4 flex justify-end">
          <Button variant="secondary" size="sm" onClick={onClose}>
            Cancel
          </Button>
        </div>
      </div>
    </div>
  );
}
