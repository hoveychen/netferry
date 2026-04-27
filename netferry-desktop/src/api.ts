import { invoke } from "@tauri-apps/api/core";
import type { ConnectionStatus, DestinationPriorities, DestinationRoutes, GlobalSettings, MethodFeatures, Profile, ProfileGroup, SshHostEntry, UpdateInfo } from "@/types";

export function listProfiles() {
  return invoke<Profile[]>("list_profiles");
}

export function saveProfile(profile: Profile) {
  return invoke<Profile[]>("save_profile", { profile });
}

export function deleteProfile(profileId: string) {
  return invoke<Profile[]>("delete_profile", { profileId });
}

export function importSshHosts() {
  return invoke<SshHostEntry[]>("import_ssh_hosts");
}

export function getDefaultIdentityFile() {
  return invoke<string | null>("get_default_identity_file");
}

/**
 * `group` + `children` are passed together for multi-tunnel (group) mode.
 * Backend writes the children to a temp group.json and spawns the Go tunnel
 * with `--group <path>`, which brings up one SSH connection per child.
 * Solo mode (single tunnel) leaves both undefined.
 */
export function connectProfile(
  profile: Profile,
  group?: ProfileGroup,
  children?: Profile[],
) {
  return invoke<ConnectionStatus>("connect_profile", {
    profile,
    group: group ?? null,
    children: children ?? null,
  });
}

export function disconnectProfile() {
  return invoke<ConnectionStatus>("disconnect_profile");
}

export function getConnectionStatus() {
  return invoke<ConnectionStatus>("get_connection_status");
}

export function getGlobalSettings() {
  return invoke<GlobalSettings>("get_global_settings");
}

export function saveGlobalSettings(settings: GlobalSettings) {
  return invoke<void>("save_global_settings", { settings });
}

export function getPriorities() {
  return invoke<DestinationPriorities>("get_priorities");
}

export function savePriorities(priorities: DestinationPriorities) {
  return invoke<void>("save_priorities", { priorities });
}

export function getRoutes() {
  return invoke<DestinationRoutes>("get_routes");
}

export function saveRoutes(routes: DestinationRoutes) {
  return invoke<void>("save_routes", { routes });
}

export function getStatsUrl() {
  return invoke<string | null>("get_stats_url");
}

// ── Profile groups ──

export function listGroups() {
  return invoke<ProfileGroup[]>("list_groups");
}

export function getGroup(groupId: string) {
  return invoke<ProfileGroup | null>("get_group", { groupId });
}

export function saveGroup(group: ProfileGroup) {
  return invoke<void>("save_group", { group });
}

export function deleteGroup(groupId: string) {
  return invoke<void>("delete_group", { groupId });
}

export function addProfileToGroup(groupId: string, profileId: string) {
  return invoke<ProfileGroup>("add_profile_to_group", { groupId, profileId });
}

export function removeProfileFromGroup(groupId: string, profileId: string) {
  return invoke<ProfileGroup>("remove_profile_from_group", { groupId, profileId });
}

export function listMethodFeatures() {
  return invoke<MethodFeatures>("list_method_features");
}

export function updateTrayInfo(displayMode: string, rxBytesPerSec: number, txBytesPerSec: number, activeConns: number) {
  return invoke<void>("update_tray_info", { displayMode, rxBytesPerSec, txBytesPerSec, activeConns });
}

export function exportProfile(profile: Profile) {
  return invoke<string>("export_profile", { profile });
}

export function exportProfileToFile(profile: Profile, path: string) {
  return invoke<void>("export_profile_to_file", { profile, path });
}

export function importProfile(data: string) {
  return invoke<Profile[]>("import_profile", { data });
}

export function importProfileFromFile(path: string) {
  return invoke<Profile[]>("import_profile_from_file", { path });
}

export type HelperStatus = "enabled" | "requires_approval" | "not_registered" | "not_found" | "os_too_old" | "not_macos";

export function getHelperStatus() {
  return invoke<HelperStatus>("get_helper_status");
}

export function registerHelper() {
  return invoke<boolean>("register_helper");
}

export function getAppVersion() {
  return invoke<string>("get_app_version");
}

export function getTunnelVersion() {
  return invoke<string>("get_tunnel_version");
}

export function checkForUpdate() {
  return invoke<UpdateInfo>("check_for_update");
}

// ── Traceroute (nexttrace sidecar) ──

export interface Hop {
  sessionId: string;
  ttl: number;
  ip?: string;
  hostname?: string;
  rttMs?: number;
  asn?: string;
  owner?: string;
  country?: string;
  province?: string;
  city?: string;
  isp?: string;
  timeout: boolean;
}

export interface TracerouteDone {
  sessionId: string;
  exitCode: number | null;
}

export function startTraceroute(opts: {
  target: string;
  maxHops?: number;
  queries?: number;
  geoSource?: string;
}) {
  return invoke<string>("start_traceroute", {
    target: opts.target,
    maxHops: opts.maxHops ?? null,
    queries: opts.queries ?? null,
    geoSource: opts.geoSource ?? null,
  });
}

export function cancelTraceroute(sessionId: string) {
  return invoke<void>("cancel_traceroute", { sessionId });
}

export interface InstallStatus {
  installed: boolean;
  path: string;
  /** "env" | "appdata" | "dev" | "" (empty when not installed) */
  source: string;
  version: string;
  expectedPath: string;
  downloadUrl: string;
}

export interface DownloadProgress {
  bytes: number;
  total: number;
  /** "downloading" | "done" | "error" */
  phase: string;
  message?: string;
}

export function nexttraceStatus() {
  return invoke<InstallStatus>("nexttrace_status");
}

export function ensureNexttraceInstalled() {
  return invoke<InstallStatus>("ensure_nexttrace_installed");
}
