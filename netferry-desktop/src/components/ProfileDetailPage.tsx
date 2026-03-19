import { useEffect, useMemo, useState } from "react";
import { ArrowLeft, Trash2 } from "lucide-react";
import type { Profile } from "@/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";

interface Props {
  profile: Profile;
  isNew: boolean;
  onBack: () => void;
  onSave: (profile: Profile) => Promise<void>;
  onDelete: (id: string) => Promise<void>;
}

export function ProfileDetailPage({ profile, isNew, onBack, onSave, onDelete }: Props) {
  const [saving, setSaving] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [showAdvanced, setShowAdvanced] = useState(false);
  const [draft, setDraft] = useState<Profile>(profile);
  const [subnetsText, setSubnetsText] = useState(profile.subnets.join(","));
  const [excludeSubnetsText, setExcludeSubnetsText] = useState(
    profile.excludeSubnets.join(","),
  );

  useEffect(() => {
    setDraft(profile);
    setSubnetsText(profile.subnets.join(","));
    setExcludeSubnetsText(profile.excludeSubnets.join(","));
  }, [profile.id]);

  const cidrRegex = /^(\d{1,3}\.){3}\d{1,3}\/\d{1,2}$/;
  const remoteRegex = /^[^@\s]+@[^:\s]+(:\d{1,5})?$/;

  const validation = useMemo(() => {
    const errors: string[] = [];
    if (!draft.name.trim()) errors.push("Profile name is required.");
    if (!remoteRegex.test(draft.remote.trim()))
      errors.push("SSH remote must be in the format user@host[:port].");
    if (draft.subnets.length === 0) errors.push("At least one included subnet is required.");
    const badInclude = draft.subnets.find((s) => !cidrRegex.test(s));
    if (badInclude) errors.push(`Invalid include subnet: ${badInclude}`);
    const badExclude = draft.excludeSubnets.find((s) => !cidrRegex.test(s));
    if (badExclude) errors.push(`Invalid exclude subnet: ${badExclude}`);
    if (draft.dns === "specific" && !draft.dnsTarget?.trim())
      errors.push("DNS target is required when DNS mode is specific.");
    return { valid: errors.length === 0, errors };
  }, [draft]);

  const setField = <K extends keyof Profile>(key: K, value: Profile[K]) =>
    setDraft((prev) => ({ ...prev, [key]: value }));

  const save = async () => {
    if (!validation.valid) return;
    setSaving(true);
    try {
      await onSave(draft);
      if (isNew) onBack();
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="flex h-screen flex-col bg-slate-100">
      {/* Header */}
      <div className="flex items-center gap-3 border-b border-slate-200 bg-white px-6 py-4">
        <button
          type="button"
          className="flex items-center gap-1 text-sm text-slate-500 hover:text-slate-800"
          onClick={onBack}
        >
          <ArrowLeft className="h-4 w-4" />
          Back
        </button>
        <span className="text-slate-300">/</span>
        <h1 className="text-base font-semibold text-slate-800">
          {isNew ? "New Profile" : draft.name || "Edit Profile"}
        </h1>
        <div className="ml-auto flex gap-2">
          {!isNew && !confirmDelete && (
            <Button
              variant="danger"
              size="sm"
              onClick={() => setConfirmDelete(true)}
            >
              <Trash2 className="mr-1 h-4 w-4" />
              Delete
            </Button>
          )}
          {!isNew && confirmDelete && (
            <>
              <span className="text-sm text-rose-600">Sure?</span>
              <Button
                variant="danger"
                size="sm"
                onClick={async () => {
                  await onDelete(draft.id);
                  onBack();
                }}
              >
                Yes, delete
              </Button>
              <Button
                variant="outline"
                size="sm"
                onClick={() => setConfirmDelete(false)}
              >
                Cancel
              </Button>
            </>
          )}
          <Button size="sm" onClick={save} disabled={saving || !validation.valid}>
            {saving ? "Saving..." : isNew ? "Create" : "Save"}
          </Button>
        </div>
      </div>

      {/* Form */}
      <div className="flex-1 overflow-y-auto p-6">
        <div className="mx-auto max-w-2xl">
          {validation.errors.length > 0 && (
            <div className="mb-4 rounded-md border border-rose-200 bg-rose-50 px-3 py-2 text-sm text-rose-700">
              {validation.errors.map((e) => (
                <p key={e}>{e}</p>
              ))}
            </div>
          )}

          <div className="rounded-xl border border-slate-200 bg-white p-6">
            <div className="grid grid-cols-2 gap-4">
              <div className="col-span-2">
                <label className="mb-1 block text-sm font-medium text-slate-700">Name</label>
                <Input
                  value={draft.name}
                  onChange={(e) => setField("name", e.target.value)}
                  placeholder="e.g. Corp Network"
                />
              </div>

              <div className="col-span-2">
                <label className="mb-1 block text-sm font-medium text-slate-700">
                  SSH Remote <span className="text-slate-400">(user@host:port)</span>
                </label>
                <Input
                  value={draft.remote}
                  onChange={(e) => setField("remote", e.target.value)}
                  placeholder="alice@example.com:22"
                />
              </div>

              <div className="col-span-2">
                <label className="mb-1 block text-sm font-medium text-slate-700">
                  Identity File
                </label>
                <Input
                  value={draft.identityFile}
                  onChange={(e) => setField("identityFile", e.target.value)}
                  placeholder="~/.ssh/id_ed25519"
                />
              </div>

              <div className="col-span-2">
                <label className="mb-1 block text-sm font-medium text-slate-700">
                  Include Subnets <span className="text-slate-400">(comma-separated)</span>
                </label>
                <Input
                  value={subnetsText}
                  onChange={(e) => {
                    const text = e.target.value;
                    setSubnetsText(text);
                    setField(
                      "subnets",
                      text.split(",").map((v) => v.trim()).filter(Boolean),
                    );
                  }}
                  placeholder="0.0.0.0/0"
                />
              </div>

              <div>
                <label className="mb-1 block text-sm font-medium text-slate-700">DNS Mode</label>
                <Select
                  value={draft.dns}
                  onChange={(e) => {
                    const mode = e.target.value as Profile["dns"];
                    setField("dns", mode);
                    if (mode !== "specific") setField("dnsTarget", undefined);
                  }}
                >
                  <option value="off">off — no DNS tunneling</option>
                  <option value="all">all — tunnel all DNS</option>
                  <option value="specific">specific — custom DNS server</option>
                </Select>
              </div>

              {draft.dns === "specific" && (
                <div>
                  <label className="mb-1 block text-sm font-medium text-slate-700">
                    DNS Target
                  </label>
                  <Input
                    value={draft.dnsTarget ?? ""}
                    onChange={(e) => setField("dnsTarget", e.target.value || undefined)}
                    placeholder="8.8.8.8:53"
                  />
                </div>
              )}
            </div>
          </div>

          {/* Advanced */}
          <button
            className="mt-4 text-sm font-medium text-slate-500 hover:text-slate-800"
            type="button"
            onClick={() => setShowAdvanced((s) => !s)}
          >
            {showAdvanced ? "Hide Advanced Options ▲" : "Show Advanced Options ▼"}
          </button>

          {showAdvanced && (
            <div className="mt-3 rounded-xl border border-slate-200 bg-white p-6">
              <div className="grid grid-cols-2 gap-4">
                <div className="col-span-2">
                  <label className="mb-1 block text-sm font-medium text-slate-700">
                    Exclude Subnets <span className="text-slate-400">(comma-separated)</span>
                  </label>
                  <Input
                    value={excludeSubnetsText}
                    onChange={(e) => {
                      const text = e.target.value;
                      setExcludeSubnetsText(text);
                      setField(
                        "excludeSubnets",
                        text.split(",").map((v) => v.trim()).filter(Boolean),
                      );
                    }}
                  />
                </div>

                <div>
                  <label className="mb-1 block text-sm font-medium text-slate-700">Method</label>
                  <Select value={draft.method} onChange={(e) => setField("method", e.target.value)}>
                    {["auto", "nat", "nft", "tproxy", "pf", "ipfw", "windivert"].map((m) => (
                      <option key={m} value={m}>
                        {m}
                      </option>
                    ))}
                  </Select>
                </div>

                <div className="flex flex-col justify-end gap-2">
                  <label className="inline-flex items-center gap-2 text-sm text-slate-700">
                    <input
                      type="checkbox"
                      checked={draft.autoNets}
                      onChange={(e) => setField("autoNets", e.target.checked)}
                    />
                    Auto-nets
                  </label>
                  <label className="inline-flex items-center gap-2 text-sm text-slate-700">
                    <input
                      type="checkbox"
                      checked={draft.disableIpv6}
                      onChange={(e) => setField("disableIpv6", e.target.checked)}
                    />
                    Disable IPv6
                  </label>
                </div>

                <div className="col-span-2">
                  <label className="inline-flex items-center gap-2 text-sm text-slate-700">
                    <input
                      type="checkbox"
                      checked={draft.autoExcludeLan}
                      onChange={(e) => setField("autoExcludeLan", e.target.checked)}
                    />
                    <span>
                      Auto-exclude LAN
                      <span className="ml-1 text-xs text-slate-400">
                        (exclude local /16 subnets from the tunnel)
                      </span>
                    </span>
                  </label>
                </div>

                <div>
                  <label className="mb-1 block text-sm font-medium text-slate-700">
                    Latency Buffer Size
                    <span className="ml-1 text-xs text-slate-400">(bytes, default 2097152)</span>
                  </label>
                  <Input
                    type="number"
                    value={draft.latencyBufferSize ?? ""}
                    onChange={(e) =>
                      setField(
                        "latencyBufferSize",
                        e.target.value ? Number(e.target.value) : undefined,
                      )
                    }
                    placeholder="2097152"
                  />
                </div>

                <div>
                  <label className="mb-1 block text-sm font-medium text-slate-700">
                    Remote Python
                  </label>
                  <Input
                    value={draft.remotePython ?? ""}
                    onChange={(e) => setField("remotePython", e.target.value || undefined)}
                    placeholder="/usr/bin/python3"
                  />
                </div>

                <div className="col-span-2">
                  <label className="mb-1 block text-sm font-medium text-slate-700">
                    Extra SSH Options
                  </label>
                  <Input
                    value={draft.extraSshOptions ?? ""}
                    onChange={(e) => setField("extraSshOptions", e.target.value || undefined)}
                    placeholder="-J jump@host"
                  />
                </div>

                <div className="col-span-2">
                  <label className="mb-1 block text-sm font-medium text-slate-700">Notes</label>
                  <Textarea
                    value={draft.notes ?? ""}
                    onChange={(e) => setField("notes", e.target.value || undefined)}
                  />
                </div>
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
