import { useEffect, useMemo, useState } from "react";
import { listen } from "@tauri-apps/api/event";
import { ConnectionPanel } from "@/components/ConnectionPanel";
import { FirstLaunchWizard } from "@/components/FirstLaunchWizard";
import { ProfileEditor } from "@/components/ProfileEditor";
import { ProfileList } from "@/components/ProfileList";
import { SshConfigImporter } from "@/components/SshConfigImporter";
import { saveProfile } from "@/api";
import { useConnectionStore } from "@/stores/connectionStore";
import { useProfileStore } from "@/stores/profileStore";
import type { ConnectionStatus, Profile } from "@/types";

const FIRST_LAUNCH_DISMISSED_KEY = "netferry_first_launch_dismissed";

function createEmptyProfile(): Profile {
  return {
    id: crypto.randomUUID(),
    name: "New Profile",
    color: "#334155",
    remote: "",
    identityFile: "",
    subnets: ["0.0.0.0/0"],
    dns: "off",
    autoConnect: false,
    excludeSubnets: [],
    autoNets: false,
    method: "auto",
    disableIpv6: false,
    notes: "",
  };
}

function App() {
  const [importerOpen, setImporterOpen] = useState(false);
  const [wizardOpen, setWizardOpen] = useState(false);
  const {
    profiles,
    selectedProfileId,
    loading,
    loadProfiles,
    selectProfile,
    createProfile,
    updateProfile,
    removeProfile,
  } = useProfileStore();
  const { status, logs, syncStatus, connect, disconnect, pushLog, setStatus } =
    useConnectionStore();

  const selectedProfile = useMemo(
    () => profiles.find((p) => p.id === selectedProfileId) ?? null,
    [profiles, selectedProfileId],
  );

  useEffect(() => {
    loadProfiles();
    syncStatus();
  }, [loadProfiles, syncStatus]);

  useEffect(() => {
    if (loading) {
      return;
    }
    const dismissed = localStorage.getItem(FIRST_LAUNCH_DISMISSED_KEY) === "1";
    setWizardOpen(profiles.length === 0 && !dismissed);
  }, [profiles.length, loading]);

  useEffect(() => {
    const offStatus = listen<ConnectionStatus>("connection-status", (event) => {
      setStatus(event.payload);
    });
    const offLog = listen<string>("connection-log", (event) => {
      pushLog(event.payload);
    });
    return () => {
      offStatus.then((fn) => fn());
      offLog.then((fn) => fn());
    };
  }, [setStatus, pushLog]);

  return (
    <main className="grid h-screen grid-cols-[280px_1fr_360px] gap-4 bg-slate-100 p-4">
      <ProfileList
        profiles={profiles}
        selectedProfileId={selectedProfileId}
        onCreate={createProfile}
        onSelect={selectProfile}
        onDelete={(id) => removeProfile(id)}
      />
      <ProfileEditor
        profile={selectedProfile}
        onSave={updateProfile}
        onOpenImporter={() => setImporterOpen(true)}
      />
      <ConnectionPanel
        status={status}
        activeProfile={selectedProfile}
        logs={logs}
        onConnect={async () => {
          if (selectedProfile) {
            await connect(selectedProfile);
          }
        }}
        onDisconnect={disconnect}
      />
      <SshConfigImporter
        open={importerOpen}
        onClose={() => setImporterOpen(false)}
        onApply={async (partial) => {
          const base = selectedProfile ?? createEmptyProfile();
          const next = { ...base, ...partial };
          await saveProfile(next);
          await loadProfiles();
          selectProfile(next.id);
          localStorage.setItem(FIRST_LAUNCH_DISMISSED_KEY, "1");
          setWizardOpen(false);
        }}
      />
      <FirstLaunchWizard
        open={wizardOpen}
        onImportFromSshConfig={() => {
          setWizardOpen(false);
          setImporterOpen(true);
        }}
        onCreateEmpty={async () => {
          const profile = createEmptyProfile();
          await saveProfile(profile);
          await loadProfiles();
          selectProfile(profile.id);
          localStorage.setItem(FIRST_LAUNCH_DISMISSED_KEY, "1");
          setWizardOpen(false);
        }}
        onSkip={() => {
          localStorage.setItem(FIRST_LAUNCH_DISMISSED_KEY, "1");
          setWizardOpen(false);
        }}
      />
    </main>
  );
}

export default App;
