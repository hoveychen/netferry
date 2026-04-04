import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { listen } from "@tauri-apps/api/event";
import { Globe, Network, Settings } from "lucide-react";
import { ConnectionPage } from "@/components/ConnectionPage";
import { DestinationsPage } from "@/components/DestinationsPage";
import { GlobalSettingsPage } from "@/components/GlobalSettingsPage";
import { HelperSetupGuide } from "@/components/HelperSetupGuide";
import { NewProfileDialog } from "@/components/NewProfileDialog";
import { ProfileDetailPage } from "@/components/ProfileDetailPage";
import { ProfileList } from "@/components/ProfileList";
import { SshConfigImporter } from "@/components/SshConfigImporter";
import { useConnectionStore } from "@/stores/connectionStore";
import { useProfileStore } from "@/stores/profileStore";
import { useRuleStore } from "@/stores/ruleStore";
import { useSettingsStore } from "@/stores/settingsStore";
import { getHelperStatus, importProfile, importProfileFromFile } from "@/api";
import type { ConnectionStatus, DeployProgress, Profile, TunnelError } from "@/types";

// Sub-page state for profile detail (pushed on top of nav).
type SubPage =
  | null
  | { kind: "detail"; profile: Profile; isNew: boolean };

type NavTab = "profiles" | "destinations" | "settings";

function App() {
  const { t } = useTranslation();
  const [activeTab, setActiveTab] = useState<NavTab>("profiles");
  const [subPage, setSubPage] = useState<SubPage>(null);
  const [newProfileDialogOpen, setNewProfileDialogOpen] = useState(false);
  const [sshImporterOpen, setSshImporterOpen] = useState(false);
  const [showHelperSetup, setShowHelperSetup] = useState<boolean | null>(null);

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
    destinations,
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

  const { loadRules } = useRuleStore();

  // Initial load
  useEffect(() => {
    loadProfiles();
    loadSettings();
    loadRules();
    syncStatus();
  }, [loadProfiles, loadSettings, loadRules, syncStatus]);

  // Check if helper setup guide should be shown (macOS only, first time)
  useEffect(() => {
    const HELPER_SETUP_KEY = "netferry_helper_setup_done";
    if (localStorage.getItem(HELPER_SETUP_KEY)) {
      setShowHelperSetup(false);
      return;
    }
    getHelperStatus().then((status) => {
      if (status === "not_macos" || status === "os_too_old" || status === "enabled") {
        localStorage.setItem(HELPER_SETUP_KEY, "1");
        setShowHelperSetup(false);
      } else {
        setShowHelperSetup(true);
      }
    }).catch(() => setShowHelperSetup(false));
  }, []);

  // Auto-connect on startup
  useEffect(() => {
    if (profilesLoading) return;
    if (status.state !== "disconnected") return;
    const profileId = settings.autoConnectProfileId;
    if (!profileId) return;
    const profile = profiles.find((p) => p.id === profileId);
    if (!profile) return;
    connect(profile);
  }, [profilesLoading]); // eslint-disable-line react-hooks/exhaustive-deps

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
    const offStatsPort = listen<number>("stats-port", (event) => {
      startSSE(`http://127.0.0.1:${event.payload}`);
    });
    const offDeployProgress = listen<DeployProgress>("deploy-progress", (event) => {
      setDeployProgress(event.payload);
    });
    const offDeployReason = listen<string>("deploy-reason", (event) => {
      setDeployReason(event.payload);
    });
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

  const handleDisconnect = async () => {
    await disconnect();
  };

  const handleConnect = async (profile: Profile) => {
    await connect(profile);
  };

  const activeProfile =
    status.profileId ? profiles.find((p) => p.id === status.profileId) ?? null : null;

  const isConnected = status.state === "connected" || status.state === "connecting" || status.state === "reconnecting";

  // Show helper setup guide on first launch (macOS)
  if (showHelperSetup === null) {
    return <div className="h-screen bg-[#1c1c1e]" />;
  }
  if (showHelperSetup) {
    return (
      <HelperSetupGuide
        onDone={() => {
          localStorage.setItem("netferry_helper_setup_done", "1");
          setShowHelperSetup(false);
        }}
      />
    );
  }

  // Profile detail page (pushed on top).
  if (subPage?.kind === "detail") {
    return (
      <ProfileDetailPage
        profile={subPage.profile}
        isNew={subPage.isNew}
        onBack={() => setSubPage(null)}
        onSave={async (saved) => {
          await updateProfile(saved);
          await loadProfiles();
          setSubPage(null);
        }}
        onDelete={async (id) => {
          await removeProfile(id);
          setSubPage(null);
        }}
      />
    );
  }

  // Connected page (shown as overlay when tunnel is active).
  if (isConnected) {
    return (
      <ConnectionPage
        status={status}
        activeProfile={activeProfile}
        logs={logs}
        tunnelStats={tunnelStats}
        activeConnections={activeConnections}
        recentClosed={recentClosed}
        tunnelErrors={tunnelErrors}
        destinations={destinations}
        deployProgress={deployProgress}
        deployReason={deployReason}
        onDisconnect={handleDisconnect}
      />
    );
  }

  // Main layout: nav bar + tab content.
  const navItems: { id: NavTab; label: string; icon: typeof Globe }[] = [
    { id: "profiles", label: t("nav.profiles"), icon: Network },
    { id: "destinations", label: t("nav.destinations"), icon: Globe },
    { id: "settings", label: t("nav.settings"), icon: Settings },
  ];

  return (
    <div className="flex h-screen flex-col bg-[#1c1c1e]">
      {/* Tab content */}
      <div className="min-h-0 flex-1 overflow-hidden">
        {activeTab === "profiles" && (
          <>
            <ProfileList
              profiles={profiles}
              onNew={() => setNewProfileDialogOpen(true)}
              onConnect={handleConnect}
              onEdit={(id) => {
                const profile = profiles.find((p) => p.id === id);
                if (profile) setSubPage({ kind: "detail", profile, isNew: false });
              }}
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
                setSubPage({ kind: "detail", profile, isNew: true });
              }}
              onImportSsh={() => setSshImporterOpen(true)}
            />

            <SshConfigImporter
              open={sshImporterOpen}
              onClose={() => setSshImporterOpen(false)}
              onImport={(profile) => {
                setSubPage({ kind: "detail", profile, isNew: true });
              }}
            />
          </>
        )}

        {activeTab === "destinations" && (
          <DestinationsPage />
        )}

        {activeTab === "settings" && (
          <GlobalSettingsPage
            settings={settings}
            profiles={profiles}
            onBack={() => setActiveTab("profiles")}
            onSave={updateSettings}
          />
        )}
      </div>

      {/* Bottom navigation bar */}
      <nav className="flex items-center justify-around border-t border-white/[0.06] bg-[#1c1c1e]/95 backdrop-blur-xl px-2 py-1.5">
        {navItems.map(({ id, label, icon: Icon }) => {
          const active = activeTab === id;
          return (
            <button
              key={id}
              onClick={() => setActiveTab(id)}
              className={`flex flex-col items-center gap-0.5 rounded-lg px-4 py-1 transition-all ${
                active
                  ? "text-[#0a84ff]"
                  : "text-white/30 hover:text-white/50"
              }`}
            >
              <Icon size={18} strokeWidth={active ? 2.2 : 1.5} />
              <span className={`text-[10px] font-medium ${active ? "text-[#0a84ff]" : ""}`}>
                {label}
              </span>
            </button>
          );
        })}
      </nav>
    </div>
  );
}

export default App;
