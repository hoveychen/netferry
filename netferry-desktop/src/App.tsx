import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { listen } from "@tauri-apps/api/event";
import { Activity, Globe, Network, PanelLeft, PanelLeftClose, Settings } from "lucide-react";
import { ConnectionPage } from "@/components/ConnectionPage";
import { DestinationsPage } from "@/components/DestinationsPage";
import { GlobalSettingsPage } from "@/components/GlobalSettingsPage";
import { HelperSetupGuide } from "@/components/HelperSetupGuide";
import { UpdateBanner } from "@/components/UpdateBanner";
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
import dragStyles from "@/drag.module.css";

// Sub-page state for profile detail (pushed on top of nav).
type SubPage =
  | null
  | { kind: "detail"; profile: Profile; isNew: boolean };

type NavTab = "profiles" | "destinations" | "settings" | "connection";

function App() {
  const { t } = useTranslation();
  const [activeTab, setActiveTab] = useState<NavTab>("profiles");
  const [subPage, setSubPage] = useState<SubPage>(null);
  const [newProfileDialogOpen, setNewProfileDialogOpen] = useState(false);
  const [sshImporterOpen, setSshImporterOpen] = useState(false);
  const [showHelperSetup, setShowHelperSetup] = useState<boolean | null>(null);
  const [sidebarExpanded, setSidebarExpanded] = useState(true);
  const prevConnected = useRef(false);

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
    // ── Menu bar events ──
    const offMenuNavigate = listen<string>("menu-navigate", (event) => {
      setActiveTab(event.payload as NavTab);
      setSubPage(null);
    });
    const offMenuCheckUpdates = listen("menu-check-updates", () => {
      // Clear dismissed version so the banner re-appears, then force a check.
      localStorage.removeItem("netferry_update_dismissed");
      // Dispatch a custom DOM event the UpdateBanner can listen for.
      window.dispatchEvent(new Event("force-update-check"));
    });
    const offMenuImportFile = listen("menu-import-file", () => {
      setActiveTab("profiles");
      setSubPage(null);
      // Small delay to ensure ProfileList is mounted before triggering import.
      setTimeout(() => window.dispatchEvent(new Event("menu-open-import")), 50);
    });
    const offMenuImportSsh = listen("menu-import-ssh", () => {
      setActiveTab("profiles");
      setSubPage(null);
      setSshImporterOpen(true);
    });
    return () => {
      offStatus.then((fn) => fn());
      offLog.then((fn) => fn());
      offError.then((fn) => fn());
      offStatsPort.then((fn) => fn());
      offDeployProgress.then((fn) => fn());
      offDeployReason.then((fn) => fn());
      offImportFile.then((fn) => fn());
      offMenuNavigate.then((fn) => fn());
      offMenuCheckUpdates.then((fn) => fn());
      offMenuImportFile.then((fn) => fn());
      offMenuImportSsh.then((fn) => fn());
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

  // Auto-collapse sidebar and switch to connection tab when connecting;
  // auto-expand and switch back when disconnected.
  useEffect(() => {
    if (isConnected && !prevConnected.current) {
      setSidebarExpanded(false);
      setActiveTab("connection");
    } else if (!isConnected && prevConnected.current) {
      setSidebarExpanded(true);
      if (activeTab === "connection") setActiveTab("profiles");
    }
    prevConnected.current = isConnected;
  }, [isConnected]); // eslint-disable-line react-hooks/exhaustive-deps

  // Drag bar for macOS overlay title bar — always present
  // Empty div that receives mousedown → Tauri's drag.js detects data-tauri-drag-region → starts window drag.
  // CSS Module preserves -webkit-app-region (Tailwind strips it).
  const dragBar = <div className={`fixed top-0 left-0 right-0 h-[38px] z-[100] ${dragStyles.dragRegion}`} data-tauri-drag-region />;

  // Show helper setup guide on first launch (macOS)
  if (showHelperSetup === null) {
    return <>{dragBar}<div className="h-screen bg-surface" /></>;
  }
  if (showHelperSetup) {
    return (
      <>
        {dragBar}
        <HelperSetupGuide
          onDone={() => {
            localStorage.setItem("netferry_helper_setup_done", "1");
            setShowHelperSetup(false);
          }}
        />
      </>
    );
  }

  // Profile detail page (pushed on top).
  if (subPage?.kind === "detail") {
    return (
      <>
        {dragBar}
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
      </>
    );
  }

  // Main layout: macOS sidebar + content.
  const navItems: { id: NavTab; label: string; icon: typeof Globe }[] = [
    ...(isConnected ? [{ id: "connection" as NavTab, label: t("nav.connection"), icon: Activity }] : []),
    { id: "profiles", label: t("nav.profiles"), icon: Network },
    { id: "destinations", label: t("nav.destinations"), icon: Globe },
    { id: "settings", label: t("nav.settings"), icon: Settings },
  ];

  const collapsed = !sidebarExpanded;

  return (
    <div className="flex h-screen flex-col bg-surface">
      {dragBar}
      <UpdateBanner />
      {/* ── Sidebar + Content ── */}
      <div className="flex min-h-0 flex-1">
      <aside className={`flex shrink-0 flex-col border-r border-sep bg-surface transition-all duration-200 ${collapsed ? "w-[72px]" : "w-[200px]"}`}>
        {/* Title bar spacer (traffic lights live here) */}
        <div className="h-[52px] shrink-0" data-tauri-drag-region />

        {/* Nav items */}
        <nav className={`flex flex-col gap-0.5 ${collapsed ? "px-1.5" : "px-3"}`}>
          {navItems.map(({ id, label, icon: Icon }) => {
            const active = activeTab === id;
            return (
              <button
                key={id}
                onClick={() => setActiveTab(id)}
                title={collapsed ? label : undefined}
                className={`flex items-center rounded-lg text-[13px] font-medium transition-all ${
                  collapsed ? "justify-center px-0 py-1.5" : "gap-2.5 px-3 py-1.5"
                } ${
                  active
                    ? "bg-ov-10 text-accent"
                    : "text-t2 hover:bg-ov-6 hover:text-t1"
                }`}
              >
                <Icon size={16} strokeWidth={active ? 2.2 : 1.6} />
                {!collapsed && label}
              </button>
            );
          })}
        </nav>

        <div className="flex-1" />

        {/* Sidebar toggle */}
        <div className={`pb-3 ${collapsed ? "px-1.5" : "px-3"}`}>
          <button
            onClick={() => setSidebarExpanded(!sidebarExpanded)}
            className={`flex w-full items-center rounded-lg py-1.5 text-t3 transition-all hover:bg-ov-6 hover:text-t2 ${
              collapsed ? "justify-center px-0" : "gap-2.5 px-3"
            }`}
          >
            {collapsed ? <PanelLeft size={16} /> : <PanelLeftClose size={16} />}
            {!collapsed && <span className="text-[13px]">{t("nav.collapseSidebar")}</span>}
          </button>
        </div>
      </aside>

      {/* ── Content area ── */}
      <main className="min-w-0 flex-1 overflow-hidden bg-sf-content">
        {activeTab === "connection" && isConnected && (
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
        )}

        {activeTab === "profiles" && (
          <>
            <ProfileList
              profiles={profiles}
              connectedProfileId={isConnected ? status.profileId : undefined}
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
      </main>
      </div>
    </div>
  );
}

export default App;
