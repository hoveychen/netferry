import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";

interface Props {
  open: boolean;
  onImportFromSshConfig: () => void;
  onCreateEmpty: () => void;
  onSkip: () => void;
}

export function FirstLaunchWizard({
  open,
  onImportFromSshConfig,
  onCreateEmpty,
  onSkip,
}: Props) {
  if (!open) {
    return null;
  }
  return (
    <div className="fixed inset-0 z-40 flex items-center justify-center bg-black/35 p-4">
      <Card className="w-full max-w-xl p-5">
        <h3 className="mb-2 text-xl font-semibold text-slate-800">Welcome to NetFerry</h3>
        <p className="mb-4 text-sm text-slate-600">
          You do not have any profiles yet. Importing from `~/.ssh/config` is the fastest way to start.
        </p>
        <div className="flex flex-wrap gap-2">
          <Button onClick={onImportFromSshConfig}>Import from SSH Config</Button>
          <Button variant="secondary" onClick={onCreateEmpty}>
            Create Empty Profile
          </Button>
          <Button variant="outline" onClick={onSkip}>
            Skip for Now
          </Button>
        </div>
      </Card>
    </div>
  );
}
