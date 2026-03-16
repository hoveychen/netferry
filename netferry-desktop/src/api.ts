import { invoke } from "@tauri-apps/api/core";
import type { ConnectionStatus, Profile, SshHostEntry } from "@/types";

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
