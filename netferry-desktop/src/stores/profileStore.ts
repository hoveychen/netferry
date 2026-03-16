import { create } from "zustand";
import { deleteProfile, listProfiles, saveProfile } from "@/api";
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
  disableIPv6: false,
  notes: "",
});

interface ProfileStore {
  profiles: Profile[];
  selectedProfileId: string | null;
  loading: boolean;
  loadProfiles: () => Promise<void>;
  selectProfile: (id: string) => void;
  createProfile: () => void;
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
    set({
      profiles,
      selectedProfileId: profiles[0]?.id ?? null,
      loading: false,
    });
  },
  selectProfile: (id) => set({ selectedProfileId: id }),
  createProfile: () => {
    const profile = newProfile();
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
