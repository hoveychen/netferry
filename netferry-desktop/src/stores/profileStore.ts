import { create } from "zustand";
import { deleteProfile, getDefaultIdentityFile, listProfiles, saveProfile } from "@/api";
import type { Profile } from "@/types";

export function newProfile(): Profile {
  return {
    id: crypto.randomUUID(),
    name: "New Profile",
    remote: "",
    identityFile: "",
    subnets: ["0.0.0.0/0"],
    dns: "all",
    excludeSubnets: [],
    autoNets: false,
    method: "auto",
    disableIpv6: false,
    enableUdp: false,
    blockUdp: true,
    notes: "",
    autoExcludeLan: true,
    poolSize: 4,
    splitConn: false,
    latencyBufferSize: 2097152,
  };
}

interface ProfileStore {
  profiles: Profile[];
  selectedProfileId: string | null;
  loading: boolean;
  loadProfiles: () => Promise<void>;
  selectProfile: (id: string) => void;
  buildBlankProfile: () => Promise<Profile>;
  buildProfileFromSsh: (partial: Partial<Profile>) => Profile;
  updateProfile: (profile: Profile) => Promise<void>;
  removeProfile: (id: string) => Promise<void>;
}

export const useProfileStore = create<ProfileStore>((set, get) => ({
  profiles: [],
  selectedProfileId: null,
  loading: false,
  loadProfiles: async () => {
    set({ loading: true });
    const profiles = await listProfiles();
    set((s) => {
      const currentId = s.selectedProfileId;
      const stillExists = profiles.some((p) => p.id === currentId);
      return {
        profiles,
        selectedProfileId: stillExists ? currentId : (profiles[0]?.id ?? null),
        loading: false,
      };
    });
  },
  selectProfile: (id) => set({ selectedProfileId: id }),
  buildBlankProfile: async () => {
    const defaultIdentityFile = await getDefaultIdentityFile().catch(() => null);
    const profile = newProfile();
    if (defaultIdentityFile) {
      profile.identityFile = defaultIdentityFile;
    }
    return profile;
  },
  buildProfileFromSsh: (partial) => {
    return { ...newProfile(), ...partial };
  },
  updateProfile: async (profile) => {
    const profiles = await saveProfile(profile);
    const selected = get().selectedProfileId ?? profile.id;
    set({ profiles, selectedProfileId: selected });
  },
  removeProfile: async (id) => {
    const profiles = await deleteProfile(id);
    set({ profiles, selectedProfileId: profiles[0]?.id ?? null });
  },
}));
