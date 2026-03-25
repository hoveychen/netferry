import { useEffect, useState } from "react";
import { listen } from "@tauri-apps/api/event";
import { ConnectionPage } from "@/components/ConnectionPage";
import { GlobalSettingsPage } from "@/components/GlobalSettingsPage";
import { NewProfileDialog } from "@/components/NewProfileDialog";
import { ProfileDetailPage } from "@/components/ProfileDetailPage";
import { ProfileList } from "@/components/ProfileList";
import { SshConfigImporter } from "@/components/SshConfigImporter";
import { useConnectionStore } from "@/stores/connectionStore";
import { useProfileStore } from "@/stores/profileStore";
import { useSettingsStore } from "@/stores/settingsStore";
import { importProfile, importProfileFromFile } from "@/api";
import type { ConnectionStatus, DeployProgress, Profile, TunnelError } from "@/types";

// Page state union
type Page =
  | { kind: "list" }
  | { kind: "detail"; profile: Profile; isNew: boolean }
  | { kind: "connected" }
  | { kind: "settings" };

function App() {
  const [page, setPage] = useState<Page>({ kind: "list" });
  const [newProfileDialogOpen, setNewProfileDialogOpen] = useState(false);
  const [sshImporterOpen, setSshImporterOpen] = useState(false);

  const {
    profiles,
    loading: profilesLoading,
    loadProfiles,
    buildBlankProfile,
    updateProfile,
    removeProfile,
  } = useProfileStore();

  const {
    settings,
    loadSettings,
    updateSettings,
  } = useSettingsStore();

  const {
    status,
    logs,
    tunnelStats,
    tunnelErrors,
    activeConnections,
    recentClosed,
    deployProgress,
    deployReason,
    syncStatus,
    connect,
    disconnect,
    pushLog,
    setStatus,
    pushTunnelError,
    setDeployProgress,
    setDeployReason,
    startSSE,
    stopSSE,
  } = useConnectionStore();

  // Initial load
  useEffect(() => {
    loadProfiles();
    loadSettings();
    syncStatus();
  }, [loadProfiles, loadSettings, syncStatus]);

  // Auto-connect on startup
  useEffect(() => {
    if (profilesLoading) return;
    if (status.state !== "disconnected") return;
    const profileId = settings.autoConnectProfileId;
    if (!profileId) return;
    const profile = profiles.find((p) => p.id === profileId);
    if (!profile) return;
    connect(profile).then(() => setPage({ kind: "connected" }));
  }, [profilesLoading]); // eslint-disable-line react-hooks/exhaustive-deps

  // If we're connected but not on the connected page, navigate there
  useEffect(() => {
    if (status.state === "connected" || status.state === "connecting" || status.state === "reconnecting") {
      setPage({ kind: "connected" });
    }
  }, [status.state]);

  // Tauri event listeners
  useEffect(() => {
    const offStatus = listen<ConnectionStatus>("connection-status", (event) => {
      setStatus(event.payload);
    });
    const offLog = listen<string>("connection-log", (event) => {
      pushLog(event.payload);
    });
    const offError = listen<TunnelError>("tunnel-error", (event) => {
      pushTunnelError(event.payload);
    });
    // stats-port is emitted by Rust when the tunnel prints its HTTP SSE port.
    const offStatsPort = listen<number>("stats-port", (event) => {
      startSSE(`http://127.0.0.1:${event.payload}`);
    });
    const offDeployProgress = listen<DeployProgress>("deploy-progress", (event) => {
      setDeployProgress(event.payload);
    });
    const offDeployReason = listen<string>("deploy-reason", (event) => {
      setDeployReason(event.payload);
    });
    // Handle .nfprofile file opened from OS (double-click / CLI arg).
    const offImportFile = listen<string>("import-profile-file", async (event) => {
      try {
        await importProfileFromFile(event.payload);
        await loadProfiles();
      } catch (err) {
        console.error("Failed to import profile from file:", err);
      }
    });
    return () => {
      offStatus.then((fn) => fn());
      offLog.then((fn) => fn());
      offError.then((fn) => fn());
      offStatsPort.then((fn) => fn());
      offDeployProgress.then((fn) => fn());
      offDeployReason.then((fn) => fn());
      offImportFile.then((fn) => fn());
      stopSSE();
    };
  }, [setStatus, pushLog, pushTunnelError, setDeployProgress, setDeployReason, startSSE, stopSSE, loadProfiles]);

  // Disconnect goes back to list
  const handleDisconnect = async () => {
    await disconnect();
    setPage({ kind: "list" });
  };

  // Connect navigates to connection page
  const handleConnect = async (profile: Profile) => {
    setPage({ kind: "connected" });
    await connect(profile);
  };

  // Find the active profile by profileId from status
  const activeProfile =
    status.profileId ? profiles.find((p) => p.id === status.profileId) ?? null : null;

  if (page.kind === "connected") {
    return (
      <ConnectionPage
        status={status}
        activeProfile={activeProfile}
        logs={logs}
        tunnelStats={tunnelStats}
        activeConnections={activeConnections}
        recentClosed={recentClosed}
        tunnelErrors={tunnelErrors}
        deployProgress={deployProgress}
        deployReason={deployReason}
        onDisconnect={handleDisconnect}
      />
    );
  }

  if (page.kind === "settings") {
    return (
      <GlobalSettingsPage
        settings={settings}
        profiles={profiles}
        onBack={() => setPage({ kind: "list" })}
        onSave={updateSettings}
      />
    );
  }

  if (page.kind === "detail") {
    return (
      <ProfileDetailPage
        profile={page.profile}
        isNew={page.isNew}
        onBack={() => setPage({ kind: "list" })}
        onSave={async (saved) => {
          await updateProfile(saved);
          await loadProfiles();
          setPage({ kind: "list" });
        }}
        onDelete={async (id) => {
          await removeProfile(id);
          setPage({ kind: "list" });
        }}
      />
    );
  }

  // Default: list page
  return (
    <>
      <ProfileList
        profiles={profiles}
        onNew={() => setNewProfileDialogOpen(true)}
        onConnect={handleConnect}
        onEdit={(id) => {
          const profile = profiles.find((p) => p.id === id);
          if (profile) setPage({ kind: "detail", profile, isNew: false });
        }}
        onOpenSettings={() => setPage({ kind: "settings" })}
        onImport={async (data) => {
          await importProfile(data);
          await loadProfiles();
        }}
        onImportFile={async (path) => {
          await importProfileFromFile(path);
          await loadProfiles();
        }}
      />

      <NewProfileDialog
        open={newProfileDialogOpen}
        onClose={() => setNewProfileDialogOpen(false)}
        onBlank={async () => {
          const profile = await buildBlankProfile();
          setPage({ kind: "detail", profile, isNew: true });
        }}
        onImportSsh={() => setSshImporterOpen(true)}
      />

      <SshConfigImporter
        open={sshImporterOpen}
        onClose={() => setSshImporterOpen(false)}
        onImport={(profile) => {
          setPage({ kind: "detail", profile, isNew: true });
        }}
      />
    </>
  );
}

export default App;
