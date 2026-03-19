import { useEffect, useState } from "react";
import { Pencil, Plus, Settings } from "lucide-react";
import type { Profile } from "@/types";
import { Button } from "@/components/ui/button";
import { countryCodeToFlag, getRegionInfo, type RegionInfo } from "@/lib/geoip";

interface Props {
  profiles: Profile[];
  onNew: () => void;
  onConnect: (profile: Profile) => void;
  onEdit: (id: string) => void;
  onOpenSettings: () => void;
}

function ProfileAvatar({ profile, region }: { profile: Profile; region?: RegionInfo }) {
  if (region?.type === "country") {
    return (
      <div className="flex h-10 w-10 items-center justify-center rounded-full bg-slate-100 text-xl">
        {countryCodeToFlag(region.countryCode)}
      </div>
    );
  }
  if (region?.type === "lan" || region?.type === "loopback") {
    return (
      <div className="flex h-10 w-10 items-center justify-center rounded-full bg-blue-100 text-lg">
        🏠
      </div>
    );
  }
  return (
    <div className="flex h-10 w-10 items-center justify-center rounded-full bg-slate-800 text-sm font-bold text-white">
      {profile.name.charAt(0).toUpperCase()}
    </div>
  );
}

export function ProfileList({ profiles, onNew, onConnect, onEdit, onOpenSettings }: Props) {
  const [regionMap, setRegionMap] = useState<Record<string, RegionInfo>>({});

  useEffect(() => {
    for (const profile of profiles) {
      getRegionInfo(profile.remote).then((info) => {
        setRegionMap((prev) => ({ ...prev, [profile.id]: info }));
      });
    }
  }, [profiles]);

  return (
    <div className="flex h-screen flex-col bg-slate-100">
      {/* Header */}
      <div className="flex items-center justify-between border-b border-slate-200 bg-white px-6 py-4">
        <h1 className="text-xl font-bold text-slate-800">NetFerry</h1>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={onOpenSettings}>
            <Settings className="h-4 w-4" />
          </Button>
          <Button size="sm" onClick={onNew}>
            <Plus className="mr-1 h-4 w-4" />
            New
          </Button>
        </div>
      </div>

      {/* Profile grid */}
      <div className="flex-1 overflow-y-auto p-6">
        {profiles.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-24 text-center">
            <p className="mb-2 text-lg font-medium text-slate-600">No profiles yet</p>
            <p className="mb-6 text-sm text-slate-400">
              Create a profile to start tunneling traffic via SSH.
            </p>
            <Button onClick={onNew}>
              <Plus className="mr-1 h-4 w-4" />
              Create Profile
            </Button>
          </div>
        ) : (
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {profiles.map((profile) => (
              <div
                key={profile.id}
                className="group relative flex cursor-pointer flex-col rounded-xl border border-slate-200 bg-white p-5 shadow-sm transition-all hover:border-slate-400 hover:shadow-md"
                onClick={() => onConnect(profile)}
              >
                {/* Edit button */}
                <button
                  type="button"
                  className="absolute right-3 top-3 rounded-md p-1.5 text-slate-300 opacity-0 transition-opacity hover:bg-slate-100 hover:text-slate-600 group-hover:opacity-100"
                  onClick={(e) => {
                    e.stopPropagation();
                    onEdit(profile.id);
                  }}
                  title="Edit profile"
                >
                  <Pencil className="h-4 w-4" />
                </button>

                <div className="mb-3 flex items-center gap-3">
                  <ProfileAvatar profile={profile} region={regionMap[profile.id]} />
                  <div className="min-w-0 flex-1">
                    <p className="truncate font-semibold text-slate-800">{profile.name}</p>
                    <p className="truncate text-xs text-slate-400">{profile.remote || "No remote set"}</p>
                  </div>
                </div>

                <div className="mt-auto pt-3 text-xs text-slate-400">
                  <span className="rounded bg-slate-100 px-2 py-0.5 font-mono">
                    DNS: {profile.dns}
                  </span>
                  {profile.autoExcludeLan && (
                    <span className="ml-2 rounded bg-slate-100 px-2 py-0.5">LAN excluded</span>
                  )}
                </div>

                <div className="mt-3 text-center text-xs font-medium text-slate-400 transition-colors group-hover:text-slate-600">
                  Click to connect →
                </div>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
