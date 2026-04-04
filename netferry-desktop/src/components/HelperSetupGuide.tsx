import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { getHelperStatus, registerHelper, type HelperStatus } from "@/api";
import { Button } from "@/components/ui/button";

interface Props {
  onDone: () => void;
}

type Step = "checking" | "needs_approval" | "registering" | "success" | "not_needed";

export function HelperSetupGuide({ onDone }: Props) {
  const { t } = useTranslation();
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
        setError(t("helper.registrationFailed"));
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
      <div className="flex h-screen flex-col items-center justify-center bg-surface px-8">
        <div className="h-6 w-6 animate-spin rounded-full border-2 border-t5 border-t-accent" />
        <p className="mt-4 text-sm text-t3">{t("helper.checkingSetup")}</p>
      </div>
    );
  }

  if (step === "not_needed") return null;

  if (step === "success") {
    return (
      <div className="flex h-screen flex-col items-center justify-center bg-surface px-8">
        <div className="mb-4 flex h-16 w-16 items-center justify-center rounded-full bg-success/20">
          <svg className="h-8 w-8 text-success" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2.5}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" />
          </svg>
        </div>
        <p className="text-lg font-semibold text-t1">{t("helper.allSet")}</p>
        <p className="mt-1 text-sm text-t3">{t("helper.backgroundRunning")}</p>
      </div>
    );
  }

  // needs_approval or registering
  return (
    <div className="flex h-screen flex-col items-center justify-center bg-surface px-8">
      <div className="w-full max-w-md">
        {/* Header */}
        <div className="mb-6 flex flex-col items-center text-center">
          <div className="mb-4 flex h-20 w-20 items-center justify-center rounded-[1.5rem] bg-gradient-to-br from-accent/20 to-[#5e5ce6]/20 ring-1 ring-bdr">
            <img src="/icon.png" alt="NetFerry" className="h-12 w-12 rounded-2xl" />
          </div>
          <h1 className="text-xl font-bold text-t1">{t("helper.oneMoreStep")}</h1>
          <p className="mt-2 max-w-sm text-sm leading-relaxed text-t3">
            {t("helper.permissionDesc")}
          </p>
        </div>

        {/* Steps */}
        <div className="mb-6 space-y-3">
          <StepCard
            number={1}
            title={t("helper.step1Title")}
            description={t("helper.step1Desc")}
          />
          <StepCard
            number={2}
            title={t("helper.step2Title")}
            description={t("helper.step2Desc")}
          />
          <StepCard
            number={3}
            title={t("helper.step3Title")}
            description={t("helper.step3Desc")}
          />
        </div>

        {error && (
          <div className="mb-4 rounded-xl border border-danger/20 bg-danger/[0.08] px-4 py-3 text-sm text-danger">
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
                <span className="mr-2 inline-block h-4 w-4 animate-spin rounded-full border-2 border-t4 border-t-t1" />
                {t("helper.registering")}
              </>
            ) : (
              t("helper.enableService")
            )}
          </Button>

          {error && (
            <Button variant="ghost" className="w-full justify-center" onClick={handleRecheck}>
              {t("helper.checkAgain")}
            </Button>
          )}

          <button
            type="button"
            className="mt-2 text-center text-xs text-t4 transition-colors hover:text-t3"
            onClick={onDone}
          >
            {t("helper.skipForNow")}
          </button>
        </div>
      </div>
    </div>
  );
}

function StepCard({ number, title, description }: { number: number; title: string; description: string }) {
  return (
    <div className="flex gap-3.5 rounded-xl border border-sep bg-ov-3 px-4 py-3">
      <div className="flex h-7 w-7 flex-shrink-0 items-center justify-center rounded-full bg-accent/15 text-xs font-bold text-accent">
        {number}
      </div>
      <div>
        <p className="text-[13px] font-medium text-t1">{title}</p>
        <p className="mt-0.5 text-[12px] leading-relaxed text-t3">{description}</p>
      </div>
    </div>
  );
}
