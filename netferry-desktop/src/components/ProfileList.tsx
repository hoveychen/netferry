import { Plus, Trash2 } from "lucide-react";
import type { Profile } from "@/types";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";

interface Props {
  profiles: Profile[];
  selectedProfileId: string | null;
  onCreate: () => void;
  onSelect: (id: string) => void;
  onDelete: (id: string) => void;
}

export function ProfileList({
  profiles,
  selectedProfileId,
  onCreate,
  onSelect,
  onDelete,
}: Props) {
  return (
    <Card className="flex h-full flex-col">
      <div className="flex items-center justify-between border-b border-slate-200 p-3">
        <h2 className="text-sm font-semibold text-slate-700">Profiles</h2>
        <Button size="sm" onClick={onCreate}>
          <Plus className="mr-1 h-4 w-4" />
          New
        </Button>
      </div>
      <div className="flex-1 overflow-y-auto p-2">
        {profiles.length === 0 ? (
          <p className="px-2 py-3 text-sm text-slate-500">No profiles yet. Click "New".</p>
        ) : (
          profiles.map((profile) => (
            <button
              key={profile.id}
              type="button"
              className={`mb-2 flex w-full items-center justify-between rounded-md border px-3 py-2 text-left text-sm ${
                selectedProfileId === profile.id
                  ? "border-slate-700 bg-slate-100"
                  : "border-slate-200 hover:bg-slate-50"
              }`}
              onClick={() => onSelect(profile.id)}
            >
              <span className="truncate">{profile.name}</span>
              <span
                className="ml-2 h-2 w-2 rounded-full"
                style={{ backgroundColor: profile.color }}
              />
              <span
                className="ml-2 text-slate-400 hover:text-rose-600"
                onClick={(e) => {
                  e.stopPropagation();
                  onDelete(profile.id);
                }}
              >
                <Trash2 className="h-4 w-4" />
              </span>
            </button>
          ))
        )}
      </div>
    </Card>
  );
}
