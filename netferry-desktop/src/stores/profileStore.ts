import { create } from "zustand";
import { deleteProfile, getDefaultIdentityFile, listProfiles, saveProfile } from "@/api";
import type { Profile } from "@/types";

const newProfile = (): Profile => ({
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
  autoExcludeLan: true,
});

interface ProfileStore {
  profiles: Profile[];
  selectedProfileId: string | null;
  loading: boolean;
  loadProfiles: () => Promise<void>;
  selectProfile: (id: string) => void;
  createProfile: () => Promise<void>;
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
  createProfile: async () => {
    const defaultIdentityFile = await getDefaultIdentityFile().catch(() => null);
    const profile = newProfile();
    if (defaultIdentityFile) {
      profile.identityFile = defaultIdentityFile;
    }
    set((s) => ({
      profiles: [...s.profiles, profile],
      selectedProfileId: profile.id,
    }));
  },
  updateProfile: async (profile) => {
    const profiles = await saveProfile(profile);
    const selected = get().selectedProfileId ?? profile.id;
    set({ profiles, selectedProfileId: selected });
  },
  removeProfile: async (id) => {
    const profiles = await deleteProfile(id);
    const nextSelected = profiles[0]?.id ?? null;
    set({ profiles, selectedProfileId: nextSelected });
  },
}));
