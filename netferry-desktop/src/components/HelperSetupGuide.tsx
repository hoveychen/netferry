import { useEffect, useState } from "react";
import { getHelperStatus, registerHelper, type HelperStatus } from "@/api";
import { Button } from "@/components/ui/button";

interface Props {
  onDone: () => void;
}

type Step = "checking" | "needs_approval" | "registering" | "success" | "not_needed";

export function HelperSetupGuide({ onDone }: Props) {
  const [step, setStep] = useState<Step>("checking");
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    checkStatus();
  }, []);

  async function checkStatus() {
    setStep("checking");
    try {
      const status: HelperStatus = await getHelperStatus();
      if (status === "enabled") {
        setStep("success");
        setTimeout(onDone, 1200);
      } else if (status === "not_macos" || status === "os_too_old") {
        setStep("not_needed");
        setTimeout(onDone, 0);
      } else {
        // not_registered, requires_approval, not_found
        setStep("needs_approval");
      }
    } catch {
      setStep("not_needed");
      onDone();
    }
  }

  async function handleEnable() {
    setStep("registering");
    setError(null);
    try {
      const ok = await registerHelper();
      if (ok) {
        setStep("success");
        setTimeout(onDone, 1200);
      } else {
        setError("Helper registration was not completed. Please try again.");
        setStep("needs_approval");
      }
    } catch (e) {
      setError(typeof e === "string" ? e : (e as Error)?.message ?? "Registration failed");
      setStep("needs_approval");
    }
  }

  async function handleRecheck() {
    setError(null);
    await checkStatus();
  }

  if (step === "checking") {
    return (
      <div className="flex h-screen flex-col items-center justify-center bg-[#1c1c1e] px-8">
        <div className="h-6 w-6 animate-spin rounded-full border-2 border-white/20 border-t-[#0a84ff]" />
        <p className="mt-4 text-sm text-white/40">Checking system setup...</p>
      </div>
    );
  }

  if (step === "not_needed") return null;

  if (step === "success") {
    return (
      <div className="flex h-screen flex-col items-center justify-center bg-[#1c1c1e] px-8">
        <div className="mb-4 flex h-16 w-16 items-center justify-center rounded-full bg-[#30d158]/20">
          <svg className="h-8 w-8 text-[#30d158]" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2.5}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" />
          </svg>
        </div>
        <p className="text-lg font-semibold text-white/90">All set!</p>
        <p className="mt-1 text-sm text-white/40">Background service is running.</p>
      </div>
    );
  }

  // needs_approval or registering
  return (
    <div className="flex h-screen flex-col items-center justify-center bg-[#1c1c1e] px-8">
      <div className="w-full max-w-md">
        {/* Header */}
        <div className="mb-6 flex flex-col items-center text-center">
          <div className="mb-4 flex h-20 w-20 items-center justify-center rounded-[1.5rem] bg-gradient-to-br from-[#0a84ff]/20 to-[#5e5ce6]/20 ring-1 ring-white/[0.1]">
            <img src="/icon.png" alt="NetFerry" className="h-12 w-12 rounded-2xl" />
          </div>
          <h1 className="text-xl font-bold text-white/90">One More Step</h1>
          <p className="mt-2 max-w-sm text-sm leading-relaxed text-white/40">
            NetFerry needs permission to run a background service for managing network tunnels without repeated password prompts.
          </p>
        </div>

        {/* Steps */}
        <div className="mb-6 space-y-3">
          <StepCard
            number={1}
            title="Click 'Enable' below"
            description="macOS will show a system dialog asking for permission."
          />
          <StepCard
            number={2}
            title="Allow in System Settings"
            description='If prompted, open System Settings → Privacy & Security → Login Items & Extensions, and enable NetFerry.'
          />
          <StepCard
            number={3}
            title="You're all set"
            description="The background service will start automatically. No more password prompts!"
          />
        </div>

        {error && (
          <div className="mb-4 rounded-xl border border-[#ff453a]/20 bg-[#ff453a]/[0.08] px-4 py-3 text-sm text-[#ff453a]">
            {error}
          </div>
        )}

        <div className="flex flex-col gap-2">
          <Button
            className="w-full justify-center"
            onClick={handleEnable}
            disabled={step === "registering"}
          >
            {step === "registering" ? (
              <>
                <span className="mr-2 inline-block h-4 w-4 animate-spin rounded-full border-2 border-white/30 border-t-white" />
                Registering...
              </>
            ) : (
              "Enable Background Service"
            )}
          </Button>

          {error && (
            <Button variant="ghost" className="w-full justify-center" onClick={handleRecheck}>
              I've enabled it — check again
            </Button>
          )}

          <button
            type="button"
            className="mt-2 text-center text-xs text-white/25 transition-colors hover:text-white/40"
            onClick={onDone}
          >
            Skip for now (will use password prompt instead)
          </button>
        </div>
      </div>
    </div>
  );
}

function StepCard({ number, title, description }: { number: number; title: string; description: string }) {
  return (
    <div className="flex gap-3.5 rounded-xl border border-white/[0.06] bg-white/[0.03] px-4 py-3">
      <div className="flex h-7 w-7 flex-shrink-0 items-center justify-center rounded-full bg-[#0a84ff]/15 text-xs font-bold text-[#0a84ff]">
        {number}
      </div>
      <div>
        <p className="text-[13px] font-medium text-white/80">{title}</p>
        <p className="mt-0.5 text-[12px] leading-relaxed text-white/35">{description}</p>
      </div>
    </div>
  );
}
