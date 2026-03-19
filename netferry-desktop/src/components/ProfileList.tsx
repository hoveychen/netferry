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

const AVATAR_GRADIENTS = [
  "from-blue-500 to-indigo-600",
  "from-teal-500 to-cyan-600",
  "from-violet-500 to-purple-600",
  "from-rose-500 to-pink-600",
  "from-amber-500 to-orange-600",
  "from-emerald-500 to-green-600",
];

function ProfileAvatar({ profile, region }: { profile: Profile; region?: RegionInfo }) {
  if (region?.type === "country") {
    return (
      <div className="flex h-11 w-11 flex-shrink-0 items-center justify-center rounded-xl bg-white/[0.07] text-2xl shadow-[inset_0_1px_0_rgba(255,255,255,0.08)] ring-1 ring-white/[0.08]">
        {countryCodeToFlag(region.countryCode)}
      </div>
    );
  }
  if (region?.type === "lan" || region?.type === "loopback") {
    return (
      <div className="flex h-11 w-11 flex-shrink-0 items-center justify-center rounded-xl bg-white/[0.07] text-xl shadow-[inset_0_1px_0_rgba(255,255,255,0.08)] ring-1 ring-white/[0.08]">
        🏠
      </div>
    );
  }
  const idx = profile.name.charCodeAt(0) % AVATAR_GRADIENTS.length;
  return (
    <div
      className={`flex h-11 w-11 flex-shrink-0 items-center justify-center rounded-xl bg-gradient-to-br ${AVATAR_GRADIENTS[idx]} text-sm font-bold text-white shadow-lg`}
    >
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
    <div className="flex h-screen flex-col bg-[#1c1c1e]">
      {/* Toolbar */}
      <div className="flex items-center justify-between border-b border-white/[0.06] bg-[#1c1c1e]/90 px-6 py-3 backdrop-blur-xl">
        <div className="flex items-center gap-2.5">
          <img src="/icon.png" alt="NetFerry" className="h-7 w-7 rounded-lg shadow-sm" />
          <span className="text-[15px] font-semibold tracking-tight text-white/90">NetFerry</span>
        </div>
        <div className="flex items-center gap-1.5">
          <Button variant="ghost" size="sm" onClick={onOpenSettings} title="Settings">
            <Settings className="h-4 w-4" />
          </Button>
          <Button size="sm" onClick={onNew}>
            <Plus className="mr-1 h-3.5 w-3.5" />
            New
          </Button>
        </div>
      </div>

      {/* Content */}
      <div className="flex-1 overflow-y-auto p-6">
        {profiles.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-24 text-center">
            <div className="mb-5 flex h-20 w-20 items-center justify-center rounded-3xl bg-white/[0.05] ring-1 ring-white/[0.07]">
              <img src="/icon.png" alt="NetFerry" className="h-12 w-12 rounded-2xl opacity-50" />
            </div>
            <p className="mb-1.5 text-[17px] font-semibold text-white/80">No profiles yet</p>
            <p className="mb-6 max-w-xs text-sm leading-relaxed text-white/35">
              Create a profile to start tunneling traffic securely via SSH.
            </p>
            <Button onClick={onNew}>
              <Plus className="mr-1.5 h-3.5 w-3.5" />
              Create Profile
            </Button>
          </div>
        ) : (
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {profiles.map((profile) => (
              <div
                key={profile.id}
                className="group relative flex cursor-pointer flex-col rounded-2xl border border-white/[0.07] bg-white/[0.04] p-5 shadow-[inset_0_1px_0_rgba(255,255,255,0.05)] transition-all duration-200 hover:-translate-y-0.5 hover:border-white/[0.13] hover:bg-white/[0.07] hover:shadow-2xl hover:shadow-black/40"
                onClick={() => onConnect(profile)}
              >
                {/* Edit button */}
                <button
                  type="button"
                  className="absolute right-3.5 top-3.5 rounded-lg p-1.5 text-white/20 opacity-0 transition-all hover:bg-white/[0.08] hover:text-white/65 group-hover:opacity-100"
                  onClick={(e) => {
                    e.stopPropagation();
                    onEdit(profile.id);
                  }}
                  title="Edit profile"
                >
                  <Pencil className="h-3.5 w-3.5" />
                </button>

                <div className="mb-4 flex items-center gap-3">
                  <ProfileAvatar profile={profile} region={regionMap[profile.id]} />
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-[15px] font-semibold text-white/90">
                      {profile.name}
                    </p>
                    <p className="truncate text-xs text-white/38 mt-0.5">
                      {profile.remote || "No remote set"}
                    </p>
                  </div>
                </div>

                <div className="mt-auto flex flex-wrap items-center gap-1.5 border-t border-white/[0.05] pt-3">
                  <span className="rounded-md bg-white/[0.06] px-2 py-0.5 font-mono text-[11px] text-white/40">
                    DNS: {profile.dns}
                  </span>
                  {profile.autoExcludeLan && (
                    <span className="rounded-md bg-white/[0.06] px-2 py-0.5 text-[11px] text-white/40">
                      LAN excl.
                    </span>
                  )}
                  <span className="ml-auto text-[11px] text-white/20 transition-colors group-hover:text-[#0a84ff]">
                    Connect →
                  </span>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
