import { useEffect, useMemo, useState } from "react";
import { ArrowDown, ArrowLeft, ArrowUp, Plus, Trash2, X } from "lucide-react";
import type { Profile, ProfileGroup } from "@/types";
import { useGroupStore, newGroup } from "@/stores/groupStore";
import { useProfileStore } from "@/stores/profileStore";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";

interface Props {
  onBack: () => void;
}

export function GroupEditorPage({ onBack }: Props) {
  const { groups, fetch, save, remove } = useGroupStore();
  const { profiles, loadProfiles } = useProfileStore();

  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [draft, setDraft] = useState<ProfileGroup | null>(null);
  const [isNew, setIsNew] = useState(false);
  const [saving, setSaving] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [addPickerOpen, setAddPickerOpen] = useState(false);

  useEffect(() => {
    fetch();
    loadProfiles();
  }, [fetch, loadProfiles]);

  useEffect(() => {
    if (selectedId == null) {
      setDraft(null);
      setIsNew(false);
      return;
    }
    const found = groups.find((g) => g.id === selectedId);
    if (found) {
      setDraft({ ...found, children: [...found.children] });
      setIsNew(false);
    }
  }, [selectedId, groups]);

  const availableProfiles = useMemo(() => {
    if (!draft) return [] as Profile[];
    const childIds = new Set(draft.children.map((c) => c.id));
    return profiles.filter((p) => !childIds.has(p.id));
  }, [profiles, draft]);

  const handleNew = () => {
    const g = newGroup();
    setDraft(g);
    setSelectedId(g.id);
    setIsNew(true);
    setConfirmDelete(false);
  };

  const handleSelect = (id: string) => {
    setSelectedId(id);
    setConfirmDelete(false);
  };

  const handleSave = async () => {
    if (!draft) return;
    setSaving(true);
    try {
      await save(draft);
      setIsNew(false);
    } finally {
      setSaving(false);
    }
  };

  const handleDelete = async () => {
    if (!draft) return;
    if (isNew) {
      setDraft(null);
      setSelectedId(null);
      setIsNew(false);
      return;
    }
    await remove(draft.id);
    setDraft(null);
    setSelectedId(null);
    setConfirmDelete(false);
  };

  const setField = <K extends keyof ProfileGroup>(key: K, value: ProfileGroup[K]) => {
    setDraft((prev) => (prev ? { ...prev, [key]: value } : prev));
  };

  const addChild = (profile: Profile) => {
    if (!draft) return;
    setField("children", [...draft.children, profile]);
    setAddPickerOpen(false);
  };

  const removeChild = (id: string) => {
    if (!draft) return;
    setField(
      "children",
      draft.children.filter((c) => c.id !== id),
    );
  };

  const moveChild = (idx: number, delta: number) => {
    if (!draft) return;
    const next = [...draft.children];
    const target = idx + delta;
    if (target < 0 || target >= next.length) return;
    [next[idx], next[target]] = [next[target], next[idx]];
    setField("children", next);
  };

  const setDefaultChild = (id: string) => {
    if (!draft) return;
    const idx = draft.children.findIndex((c) => c.id === id);
    if (idx <= 0) return;
    const next = [...draft.children];
    const [picked] = next.splice(idx, 1);
    next.unshift(picked);
    setField("children", next);
  };

  const defaultChildId = draft?.children[0]?.id ?? null;

  return (
    <div className="flex h-screen flex-col bg-surface pt-[38px]">
      {/* Toolbar */}
      <div className="flex items-center gap-3 border-b border-sep px-6 py-3">
        <button
          type="button"
          className="flex items-center gap-1.5 text-sm text-t3 transition-colors hover:text-t1"
          onClick={onBack}
        >
          <ArrowLeft className="h-4 w-4" />
          Back
        </button>
        <span className="text-t5">/</span>
        <h1 className="text-[15px] font-semibold text-t1">Profile Groups</h1>
      </div>

      <div className="flex min-h-0 flex-1">
        {/* Left list */}
        <aside className="w-[260px] shrink-0 border-r border-sep bg-ov-2">
          <div className="flex items-center justify-between px-4 py-3">
            <span className="text-[11px] font-semibold uppercase tracking-widest text-t4">
              Groups
            </span>
            <button
              type="button"
              className="flex items-center gap-1 text-xs text-accent/80 transition-colors hover:text-accent"
              onClick={handleNew}
            >
              <Plus className="h-3.5 w-3.5" /> New
            </button>
          </div>
          <div className="flex flex-col gap-0.5 px-2">
            {groups.length === 0 && !isNew && (
              <p className="px-3 py-6 text-center text-xs text-t4">No groups yet</p>
            )}
            {groups.map((g) => (
              <button
                key={g.id}
                type="button"
                onClick={() => handleSelect(g.id)}
                className={`flex items-center justify-between rounded-lg px-3 py-2 text-left text-sm transition-colors ${
                  selectedId === g.id
                    ? "bg-ov-10 text-accent"
                    : "text-t2 hover:bg-ov-6 hover:text-t1"
                }`}
              >
                <span className="truncate">{g.name || "(unnamed)"}</span>
                <span className="ml-2 text-[11px] text-t4">{g.children.length}</span>
              </button>
            ))}
            {isNew && draft && (
              <div className="rounded-lg bg-ov-10 px-3 py-2 text-sm text-accent">
                {draft.name || "(new)"}
              </div>
            )}
          </div>
        </aside>

        {/* Right editor */}
        <main className="min-w-0 flex-1 overflow-y-auto p-6">
          {!draft ? (
            <div className="flex h-full items-center justify-center text-sm text-t4">
              Select a group or create a new one.
            </div>
          ) : (
            <div className="mx-auto max-w-2xl space-y-4">
              <div className="flex items-center justify-end gap-2">
                {!isNew && !confirmDelete && (
                  <Button variant="danger" size="sm" onClick={() => setConfirmDelete(true)}>
                    <Trash2 className="mr-1 h-3.5 w-3.5" />
                    Delete
                  </Button>
                )}
                {!isNew && confirmDelete && (
                  <>
                    <span className="text-sm text-danger/80">Delete this group?</span>
                    <Button variant="danger" size="sm" onClick={handleDelete}>
                      Yes, delete
                    </Button>
                    <Button variant="ghost" size="sm" onClick={() => setConfirmDelete(false)}>
                      Cancel
                    </Button>
                  </>
                )}
                {isNew && (
                  <Button variant="ghost" size="sm" onClick={handleDelete}>
                    Discard
                  </Button>
                )}
                <Button size="sm" onClick={handleSave} disabled={saving || !draft.name.trim()}>
                  {saving ? "Saving..." : isNew ? "Create" : "Save"}
                </Button>
              </div>

              <div className="rounded-2xl border border-sep bg-ov-3 p-6 shadow-[inset_0_1px_0_var(--inset-highlight)]">
                <p className="mb-5 text-[11px] font-semibold uppercase tracking-widest text-t4">
                  Group
                </p>
                <div>
                  <label className="mb-1.5 block text-sm font-medium text-t2">Name</label>
                  <Input
                    value={draft.name}
                    onChange={(e) => setField("name", e.target.value)}
                    placeholder="My group"
                  />
                </div>
              </div>

              <div className="rounded-2xl border border-sep bg-ov-3 p-6 shadow-[inset_0_1px_0_var(--inset-highlight)]">
                <div className="mb-4 flex items-center justify-between">
                  <p className="text-[11px] font-semibold uppercase tracking-widest text-t4">
                    Children ({draft.children.length})
                  </p>
                  <button
                    type="button"
                    className="flex items-center gap-1 text-xs text-accent/80 transition-colors hover:text-accent"
                    onClick={() => setAddPickerOpen((v) => !v)}
                  >
                    <Plus className="h-3.5 w-3.5" /> Add profile
                  </button>
                </div>

                {addPickerOpen && (
                  <div className="mb-4 rounded-xl border border-sep bg-ov-2 p-3">
                    <div className="mb-2 flex items-center justify-between">
                      <span className="text-xs text-t3">Pick a profile to add</span>
                      <button
                        type="button"
                        className="text-t4 transition-colors hover:text-t1"
                        onClick={() => setAddPickerOpen(false)}
                      >
                        <X className="h-3.5 w-3.5" />
                      </button>
                    </div>
                    {availableProfiles.length === 0 ? (
                      <p className="py-2 text-center text-xs text-t4">
                        No more profiles to add
                      </p>
                    ) : (
                      <div className="flex flex-col gap-1">
                        {availableProfiles.map((p) => (
                          <button
                            key={p.id}
                            type="button"
                            className="flex items-center justify-between rounded-md px-3 py-1.5 text-left text-sm text-t2 transition-colors hover:bg-ov-6 hover:text-t1"
                            onClick={() => addChild(p)}
                          >
                            <span className="truncate">{p.name}</span>
                            <span className="ml-2 truncate text-[11px] text-t4">
                              {p.remote}
                            </span>
                          </button>
                        ))}
                      </div>
                    )}
                  </div>
                )}

                {draft.children.length === 0 ? (
                  <p className="py-6 text-center text-xs text-t4">
                    No children. Add at least one profile.
                  </p>
                ) : (
                  <div className="space-y-2">
                    <div className="mb-3">
                      <label className="mb-1.5 block text-sm font-medium text-t2">
                        Default profile
                      </label>
                      <Select
                        value={defaultChildId ?? ""}
                        onChange={(e) => setDefaultChild(e.target.value)}
                      >
                        {draft.children.map((c) => (
                          <option key={c.id} value={c.id}>
                            {c.name}
                          </option>
                        ))}
                      </Select>
                      <p className="mt-1 text-[11px] text-t4">
                        The default profile is always listed first.
                      </p>
                    </div>

                    {draft.children.map((child, idx) => (
                      <div
                        key={child.id}
                        className="flex items-center gap-2 rounded-xl border border-sep bg-ov-2 px-3 py-2"
                      >
                        <div className="min-w-0 flex-1">
                          <div className="flex items-center gap-2">
                            <span className="truncate text-sm text-t1">{child.name}</span>
                            {idx === 0 && (
                              <span className="rounded-md bg-accent/15 px-1.5 py-0.5 text-[10px] font-medium text-accent">
                                default
                              </span>
                            )}
                          </div>
                          <p className="truncate text-[11px] text-t4">{child.remote}</p>
                        </div>
                        <button
                          type="button"
                          className="rounded-md p-1 text-t3 transition-colors hover:bg-ov-6 hover:text-t1 disabled:opacity-30"
                          disabled={idx === 0}
                          onClick={() => moveChild(idx, -1)}
                          title="Move up"
                        >
                          <ArrowUp className="h-3.5 w-3.5" />
                        </button>
                        <button
                          type="button"
                          className="rounded-md p-1 text-t3 transition-colors hover:bg-ov-6 hover:text-t1 disabled:opacity-30"
                          disabled={idx === draft.children.length - 1}
                          onClick={() => moveChild(idx, 1)}
                          title="Move down"
                        >
                          <ArrowDown className="h-3.5 w-3.5" />
                        </button>
                        <button
                          type="button"
                          className="rounded-md p-1 text-t3 transition-colors hover:bg-ov-6 hover:text-danger"
                          onClick={() => removeChild(child.id)}
                          title="Remove"
                        >
                          <X className="h-3.5 w-3.5" />
                        </button>
                      </div>
                    ))}
                  </div>
                )}
              </div>
            </div>
          )}
        </main>
      </div>
    </div>
  );
}
