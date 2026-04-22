import { create } from "zustand";
import { deleteGroup, listGroups, saveGroup } from "@/api";
import type { ProfileGroup } from "@/types";

export function newGroup(): ProfileGroup {
  return {
    id: crypto.randomUUID(),
    name: "New Group",
    children: [],
    rules: {},
    priorities: {},
  };
}

interface GroupStore {
  groups: ProfileGroup[];
  loading: boolean;
  fetch: () => Promise<void>;
  save: (group: ProfileGroup) => Promise<void>;
  remove: (id: string) => Promise<void>;
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
