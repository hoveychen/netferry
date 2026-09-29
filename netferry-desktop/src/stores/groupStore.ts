import { create } from "zustand";
import { deleteGroup, listGroups, saveGroup } from "@/api";
import { useSettingsStore } from "@/stores/settingsStore";
import type { Profile, ProfileGroup } from "@/types";

export function newGroup(): ProfileGroup {
  return {
    id: crypto.randomUUID(),
    name: "New Group",
    childrenIds: [],
  };
}

interface GroupStore {
  groups: ProfileGroup[];
  loading: boolean;
  fetch: () => Promise<void>;
  save: (group: ProfileGroup) => Promise<void>;
  remove: (id: string) => Promise<void>;
}

/** Project a group's id references onto concrete Profile objects, preserving
 *  order. Missing profiles (e.g. orphaned ids) are silently skipped. */
export function joinGroupProfiles(group: ProfileGroup, profiles: Profile[]): Profile[] {
  const byId = new Map(profiles.map((p) => [p.id, p]));
  const out: Profile[] = [];
  for (const id of group.childrenIds) {
    const p = byId.get(id);
    if (p) out.push(p);
  }
  return out;
}

export const useGroupStore = create<GroupStore>((set) => ({
  groups: [],
  loading: false,
  fetch: async () => {
    set({ loading: true });
    try {
      const groups = await listGroups();
      set({ groups, loading: false });
    } catch (err) {
      console.error("Failed to load groups:", err);
      set({ loading: false });
    }
  },
  save: async (group) => {
    await saveGroup(group);
    const groups = await listGroups();
    set({ groups });
  },
  remove: async (id) => {
    await deleteGroup(id);
    const groups = await listGroups();
    set({ groups });
  },
}));

/** The profile group selected in settings (`activeGroupId`), or null. */
export function useActiveGroup(): ProfileGroup | null {
  const activeGroupId = useSettingsStore((s) => s.settings.activeGroupId ?? null);
  return useGroupStore((s) => s.groups.find((g) => g.id === activeGroupId) ?? null);
}
