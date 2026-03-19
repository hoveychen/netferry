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
import type { ConnectionEvent, ConnectionStatus, Profile, TunnelError, TunnelStats } from "@/types";

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
    connectionEvents,
    tunnelErrors,
    syncStatus,
    connect,
    disconnect,
    pushLog,
    setStatus,
    setTunnelStats,
    pushConnectionEvent,
    pushTunnelError,
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
    if (status.state === "connected" || status.state === "connecting") {
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
    const offStats = listen<TunnelStats>("tunnel-stats", (event) => {
      setTunnelStats(event.payload);
    });
    const offConnection = listen<ConnectionEvent>("tunnel-connection", (event) => {
      pushConnectionEvent(event.payload);
    });
    const offError = listen<TunnelError>("tunnel-error", (event) => {
      pushTunnelError(event.payload);
    });
    return () => {
      offStatus.then((fn) => fn());
      offLog.then((fn) => fn());
      offStats.then((fn) => fn());
      offConnection.then((fn) => fn());
      offError.then((fn) => fn());
    };
  }, [setStatus, pushLog, setTunnelStats, pushConnectionEvent, pushTunnelError]);

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
        connectionEvents={connectionEvents}
        tunnelErrors={tunnelErrors}
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
