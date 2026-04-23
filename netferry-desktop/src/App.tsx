import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { listen } from "@tauri-apps/api/event";
import { Activity, Globe, Network, PanelLeft, PanelLeftClose, Settings } from "lucide-react";
import { ConnectionErrorDialog } from "@/components/ConnectionErrorDialog";
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
import { joinGroupProfiles, useGroupStore } from "@/stores/groupStore";
import { useProfileStore } from "@/stores/profileStore";
import { useRuleStore } from "@/stores/ruleStore";
import { useSettingsStore } from "@/stores/settingsStore";
import {
  addProfileToGroup,
  getHelperStatus,
  importProfile,
  importProfileFromFile,
} from "@/api";
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
  const prevStatusState = useRef<ConnectionStatus["state"]>("disconnected");
  const lastAttemptedProfileId = useRef<string | null>(null);
  const [errorDialog, setErrorDialog] = useState<{
    message?: string;
    errors: TunnelError[];
    profileName?: string;
  } | null>(null);

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

  const { loadRules, activeGroup, connectionMode, setConnectionMode } = useRuleStore();
  const { fetch: fetchGroups } = useGroupStore();

  /**
   * After an import call returns a new profiles array, find the id that didn't
   * exist before and attach it to the active group so it shows up in the list
   * the user is currently looking at.
   */
  const attachNewProfilesToActiveGroup = async (
    prevIds: Set<string>,
    updated: Profile[],
  ) => {
    const groupId = useRuleStore.getState().activeGroup?.id;
    if (!groupId) return;
    const newlyImported = updated.filter((p) => !prevIds.has(p.id));
    if (newlyImported.length === 0) return;
    for (const p of newlyImported) {
      try {
        await addProfileToGroup(groupId, p.id);
      } catch (err) {
        console.error("Failed to attach imported profile to group:", err);
      }
    }
    await fetchGroups();
    await loadRules();
  };

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
        const prevIds = new Set(useProfileStore.getState().profiles.map((p) => p.id));
        const updated = await importProfileFromFile(event.payload);
        await loadProfiles();
        await attachNewProfilesToActiveGroup(prevIds, updated);
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
    // Clicking a single profile disengages the group's multi-profile mode —
    // the sidecar receives a null group so ConnectionPage shows single-profile UI.
    setConnectionMode("solo");
    await connect(profile);
  };

  const handleConnectGroup = async () => {
    const group = useRuleStore.getState().activeGroup;
    if (!group) return;
    const profiles = useProfileStore.getState().profiles;
    const children = joinGroupProfiles(group, profiles);
    const seed = children[0];
    if (!seed) return;
    setConnectionMode("group");
    // Pass full group + children so the sidecar writes a temp group.json and
    // spawns the Go tunnel with --group, bringing up N SSH connections.
    await connect(seed, group, children);
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

  // Surface connection failures: when status.state transitions INTO "error",
  // snapshot the status message + accumulated tunnelErrors into a modal.
  // The sidecar clears profileId on error, so we track the last connecting
  // profile id to resolve the profile name for the dialog header.
  useEffect(() => {
    if (status.state === "connecting" && status.profileId) {
      lastAttemptedProfileId.current = status.profileId;
    }
    if (status.state === "error" && prevStatusState.current !== "error") {
      const id = status.profileId ?? lastAttemptedProfileId.current;
      const profile = id ? profiles.find((p) => p.id === id) : null;
      setErrorDialog({
        message: status.message,
        errors: tunnelErrors,
        profileName: profile?.name,
      });
    }
    prevStatusState.current = status.state;
  }, [status.state, status.message, status.profileId, tunnelErrors, profiles]);

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
    const wasNew = subPage.isNew;
    return (
      <>
        {dragBar}
        <ProfileDetailPage
          profile={subPage.profile}
          isNew={subPage.isNew}
          onBack={() => setSubPage(null)}
          onSave={async (saved) => {
            const prevIds = new Set(useProfileStore.getState().profiles.map((p) => p.id));
            await updateProfile(saved);
            await loadProfiles();
            if (wasNew) {
              await attachNewProfilesToActiveGroup(prevIds, useProfileStore.getState().profiles);
            }
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
      <main className="relative min-w-0 flex-1 overflow-hidden bg-sf-content">
        {activeTab === "connection" && isConnected && (
          <ConnectionPage
            status={status}
            activeProfile={activeProfile}
            activeGroup={activeGroup}
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
              connectedProfileId={isConnected ? status.profileId : undefined}
              groupConnected={isConnected && connectionMode === "group"}
              onNew={() => setNewProfileDialogOpen(true)}
              onConnect={handleConnect}
              onConnectGroup={handleConnectGroup}
              onEdit={(id) => {
                const profile = profiles.find((p) => p.id === id);
                if (profile) setSubPage({ kind: "detail", profile, isNew: false });
              }}
              onImport={async (data) => {
                const prevIds = new Set(useProfileStore.getState().profiles.map((p) => p.id));
                const updated = await importProfile(data);
                await loadProfiles();
                await attachNewProfilesToActiveGroup(prevIds, updated);
              }}
              onImportFile={async (path) => {
                const prevIds = new Set(useProfileStore.getState().profiles.map((p) => p.id));
                const updated = await importProfileFromFile(path);
                await loadProfiles();
                await attachNewProfilesToActiveGroup(prevIds, updated);
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

      <ConnectionErrorDialog
        open={errorDialog !== null}
        message={errorDialog?.message}
        errors={errorDialog?.errors ?? []}
        profileName={errorDialog?.profileName}
        onClose={() => setErrorDialog(null)}
      />
    </div>
  );
}

export default App;
