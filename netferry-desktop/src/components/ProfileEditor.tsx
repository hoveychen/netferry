import { useEffect, useMemo, useState } from "react";
import type { Profile } from "@/types";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";

interface Props {
  profile: Profile | null;
  onSave: (profile: Profile) => Promise<void>;
  onOpenImporter: () => void;
}

const colors = [
  "#334155",
  "#2563eb",
  "#16a34a",
  "#ea580c",
  "#a855f7",
  "#dc2626",
];

export function ProfileEditor({ profile, onSave, onOpenImporter }: Props) {
  const [saving, setSaving] = useState(false);
  const [showAdvanced, setShowAdvanced] = useState(false);
  const [draft, setDraft] = useState<Profile | null>(profile);
  const [subnetsText, setSubnetsText] = useState(profile?.subnets.join(",") ?? "");
  const [excludeSubnetsText, setExcludeSubnetsText] = useState(
    profile?.excludeSubnets.join(",") ?? "",
  );

  useEffect(() => {
    setDraft(profile);
    setSubnetsText(profile?.subnets.join(",") ?? "");
    setExcludeSubnetsText(profile?.excludeSubnets.join(",") ?? "");
  }, [profile]);

  const local = useMemo(() => draft, [draft]);

  const cidrRegex = /^(\d{1,3}\.){3}\d{1,3}\/\d{1,2}$/;
  const remoteRegex = /^[^@\s]+@[^:\s]+(:\d{1,5})?$/;

  const validation = useMemo(() => {
    if (!local) {
      return { valid: false, errors: [] as string[] };
    }
    const errors: string[] = [];
    if (!local.name.trim()) {
      errors.push("Profile name is required.");
    }
    if (!remoteRegex.test(local.remote.trim())) {
      errors.push("SSH remote must be in the format user@host[:port].");
    }
    if (local.subnets.length === 0) {
      errors.push("At least one included subnet is required.");
    }
    const invalidSubnet = local.subnets.find((s) => !cidrRegex.test(s));
    if (invalidSubnet) {
      errors.push(`Invalid include subnet format: ${invalidSubnet}`);
    }
    const invalidExclude = local.excludeSubnets.find((s) => !cidrRegex.test(s));
    if (invalidExclude) {
      errors.push(`Invalid exclude subnet format: ${invalidExclude}`);
    }
    if (local.dns === "specific" && !local.dnsTarget?.trim()) {
      errors.push("DNS target is required when DNS mode is set to specific.");
    }
    return { valid: errors.length === 0, errors };
  }, [local]);

  if (!local) {
    return (
      <Card className="flex h-full items-center justify-center">
        <p className="text-sm text-slate-500">Select or create a profile.</p>
      </Card>
    );
  }

  const setField = <K extends keyof Profile>(key: K, value: Profile[K]) => {
    setDraft((prev) => {
      if (!prev) {
        return prev;
      }
      return {
        ...prev,
        [key]: value,
      };
    });
  };

  const save = async () => {
    if (!local || !validation.valid) {
      return;
    }
    setSaving(true);
    try {
      await onSave(local);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Card className="h-full overflow-y-auto p-4">
      <div className="mb-4 flex items-center justify-between">
        <h2 className="text-lg font-semibold text-slate-800">Profile Settings</h2>
        <div className="flex gap-2">
          <Button type="button" variant="secondary" onClick={onOpenImporter}>
            Import from SSH Config
          </Button>
          <Button type="button" onClick={save} disabled={saving || !validation.valid}>
            {saving ? "Saving..." : "Save"}
          </Button>
        </div>
      </div>

      {validation.errors.length > 0 ? (
        <div className="mb-4 rounded-md border border-rose-200 bg-rose-50 px-3 py-2 text-sm text-rose-700">
          {validation.errors.map((e) => (
            <p key={e}>{e}</p>
          ))}
        </div>
      ) : null}

      <div className="grid grid-cols-2 gap-4">
        <div className="col-span-2">
          <label className="mb-1 block text-sm font-medium text-slate-700">Name</label>
          <Input
            value={local.name}
            onChange={(e) => setField("name", e.target.value)}
            placeholder="Example: Corp Network"
          />
        </div>
        <div>
          <label className="mb-1 block text-sm font-medium text-slate-700">Color</label>
          <Select value={local.color} onChange={(e) => setField("color", e.target.value)}>
            {colors.map((c) => (
              <option key={c} value={c}>
                {c}
              </option>
            ))}
          </Select>
        </div>
        <div className="flex items-end">
          <label className="inline-flex items-center gap-2 text-sm text-slate-700">
            <input
              type="checkbox"
              checked={local.autoConnect}
              onChange={(e) => setField("autoConnect", e.target.checked)}
            />
            Auto-connect on startup
          </label>
        </div>

        <div className="col-span-2">
          <label className="mb-1 block text-sm font-medium text-slate-700">
            SSH Remote (user@host:port)
          </label>
          <Input
            value={local.remote}
            onChange={(e) => setField("remote", e.target.value)}
            placeholder="alice@example.com:22"
          />
        </div>

        <div className="col-span-2">
          <label className="mb-1 block text-sm font-medium text-slate-700">Identity File</label>
          <Input
            value={local.identityFile}
            onChange={(e) => setField("identityFile", e.target.value)}
            placeholder="~/.ssh/id_ed25519"
          />
        </div>

        <div className="col-span-2">
          <label className="mb-1 block text-sm font-medium text-slate-700">
            Include Subnets (comma-separated)
          </label>
          <Input
            value={subnetsText}
            onChange={(e) => {
              const text = e.target.value;
              setSubnetsText(text);
              setField(
                "subnets",
                text
                  .split(",")
                  .map((v) => v.trim())
                  .filter(Boolean),
              );
            }}
            placeholder="0.0.0.0/0,10.0.0.0/8"
          />
        </div>

        <div>
          <label className="mb-1 block text-sm font-medium text-slate-700">DNS Mode</label>
          <Select value={local.dns} onChange={(e) => setField("dns", e.target.value as Profile["dns"])}>
            <option value="off">off</option>
            <option value="all">all</option>
            <option value="specific">specific</option>
          </Select>
        </div>
        <div>
          <label className="mb-1 block text-sm font-medium text-slate-700">DNS Target</label>
          <Input
            value={local.dnsTarget ?? ""}
            onChange={(e) => setField("dnsTarget", e.target.value || undefined)}
            placeholder="8.8.8.8:53"
          />
        </div>
      </div>

      <button
        className="mt-4 text-sm font-medium text-slate-600 hover:text-slate-900"
        type="button"
        onClick={() => setShowAdvanced((s) => !s)}
      >
        {showAdvanced ? "Hide Advanced Options" : "Show Advanced Options"}
      </button>

      {showAdvanced ? (
        <div className="mt-3 grid grid-cols-2 gap-4 rounded-md border border-slate-200 p-3">
          <div className="col-span-2">
            <label className="mb-1 block text-sm font-medium text-slate-700">
              Exclude Subnets (comma-separated)
            </label>
            <Input
              value={excludeSubnetsText}
              onChange={(e) => {
                const text = e.target.value;
                setExcludeSubnetsText(text);
                setField(
                  "excludeSubnets",
                  text
                    .split(",")
                    .map((v) => v.trim())
                    .filter(Boolean),
                );
              }}
            />
          </div>
          <div>
            <label className="mb-1 block text-sm font-medium text-slate-700">Method</label>
            <Select value={local.method} onChange={(e) => setField("method", e.target.value)}>
              {["auto", "nat", "nft", "tproxy", "pf", "ipfw", "windivert"].map((m) => (
                <option key={m} value={m}>
                  {m}
                </option>
              ))}
            </Select>
          </div>
          <div className="flex items-end gap-4">
            <label className="inline-flex items-center gap-2 text-sm text-slate-700">
              <input
                type="checkbox"
                checked={local.autoNets}
                onChange={(e) => setField("autoNets", e.target.checked)}
              />
              Auto-nets
            </label>
            <label className="inline-flex items-center gap-2 text-sm text-slate-700">
              <input
                type="checkbox"
                checked={local.disableIPv6}
                onChange={(e) => setField("disableIPv6", e.target.checked)}
              />
              Disable IPv6
            </label>
          </div>
          <div>
            <label className="mb-1 block text-sm font-medium text-slate-700">Remote Python</label>
            <Input
              value={local.remotePython ?? ""}
              onChange={(e) => setField("remotePython", e.target.value || undefined)}
              placeholder="/usr/bin/python3"
            />
          </div>
          <div>
            <label className="mb-1 block text-sm font-medium text-slate-700">
              Extra SSH Options
            </label>
            <Input
              value={local.extraSshOptions ?? ""}
              onChange={(e) => setField("extraSshOptions", e.target.value || undefined)}
              placeholder="-J jump@host"
            />
          </div>
          <div className="col-span-2">
            <label className="mb-1 block text-sm font-medium text-slate-700">Notes</label>
            <Textarea
              value={local.notes ?? ""}
              onChange={(e) => setField("notes", e.target.value || undefined)}
            />
          </div>
        </div>
      ) : null}
    </Card>
  );
}
