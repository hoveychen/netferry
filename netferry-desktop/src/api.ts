import { invoke } from "@tauri-apps/api/core";
import type { ConnectionStatus, DestinationPriorities, DestinationRoutes, GlobalSettings, MethodFeatures, Profile, SshHostEntry, UpdateInfo } from "@/types";

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

export function connectProfile(profile: Profile) {
  return invoke<ConnectionStatus>("connect_profile", { profile });
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

export function checkForUpdate() {
  return invoke<UpdateInfo>("check_for_update");
}
