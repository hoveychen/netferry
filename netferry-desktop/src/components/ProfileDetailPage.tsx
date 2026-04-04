import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { ArrowLeft, FolderOpen, Plus, Trash2, X } from "lucide-react";
import { open } from "@tauri-apps/plugin-dialog";
import type { JumpHost, MethodFeatures, Profile } from "@/types";
import { listMethodFeatures } from "@/api";
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
  const { t } = useTranslation();
  const [saving, setSaving] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [showAdvanced, setShowAdvanced] = useState(false);
  const [draft, setDraft] = useState<Profile>(profile);
  const [subnetsText, setSubnetsText] = useState(profile.subnets.join(","));
  const [excludeSubnetsText, setExcludeSubnetsText] = useState(
    profile.excludeSubnets.join(","),
  );
  const [methodFeatures, setMethodFeatures] = useState<MethodFeatures | null>(null);

  useEffect(() => {
    listMethodFeatures().then(setMethodFeatures).catch(() => setMethodFeatures(null));
  }, []);

  useEffect(() => {
    setDraft(profile);
    setSubnetsText(profile.subnets.join(","));
    setExcludeSubnetsText(profile.excludeSubnets.join(","));
  }, [profile.id]);

  // Available methods: "auto" + whatever the tunnel binary reports.
  const availableMethods = useMemo(() => {
    if (!methodFeatures) return ["auto"];
    return ["auto", ...Object.keys(methodFeatures)];
  }, [methodFeatures]);

  // Features supported by the currently selected method.
  const activeFeatures = useMemo(() => {
    if (!methodFeatures) return new Set<string>();
    if (draft.method === "auto") {
      // "auto" supports the union of all methods' features.
      const all = new Set<string>();
      for (const feats of Object.values(methodFeatures)) {
        for (const f of feats) all.add(f);
      }
      return all;
    }
    return new Set(methodFeatures[draft.method] ?? []);
  }, [methodFeatures, draft.method]);

  const cidrRegex = /^(\d{1,3}\.){3}\d{1,3}\/\d{1,2}$/;
  const remoteRegex = /^[^@\s]+@[^:\s]+(:\d{1,5})?$/;

  const validation = useMemo(() => {
    const errors: string[] = [];
    if (!draft.name.trim()) errors.push(t("profileDetail.validation.nameRequired"));
    if (!remoteRegex.test(draft.remote.trim()))
      errors.push(t("profileDetail.validation.invalidRemote"));
    if (draft.subnets.length === 0) errors.push(t("profileDetail.validation.subnetRequired"));
    const badInclude = draft.subnets.find((s) => !cidrRegex.test(s));
    if (badInclude) errors.push(t("profileDetail.validation.invalidIncludeSubnet", { subnet: badInclude }));
    const badExclude = draft.excludeSubnets.find((s) => !cidrRegex.test(s));
    if (badExclude) errors.push(t("profileDetail.validation.invalidExcludeSubnet", { subnet: badExclude }));
    if (draft.dns === "specific" && !draft.dnsTarget?.trim())
      errors.push(t("profileDetail.validation.dnsTargetRequired"));
    return { valid: errors.length === 0, errors };
  }, [draft, t]);

  const setField = <K extends keyof Profile>(key: K, value: Profile[K]) =>
    setDraft((prev) => ({ ...prev, [key]: value }));

  const jumpHosts = draft.jumpHosts ?? [];
  const setJumpHosts = (hosts: JumpHost[]) => setField("jumpHosts", hosts.length > 0 ? hosts : undefined);
  const updateJumpHost = (idx: number, patch: Partial<JumpHost>) =>
    setJumpHosts(jumpHosts.map((h, i) => (i === idx ? { ...h, ...patch } : h)));
  const removeJumpHost = (idx: number) => setJumpHosts(jumpHosts.filter((_, i) => i !== idx));
  const addJumpHost = () => setJumpHosts([...jumpHosts, { remote: "" }]);

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

  // Imported profiles: only allow renaming and deleting.
  if (profile.imported) {
    return (
      <div className="flex h-screen flex-col bg-[#1c1c1e]">
        <div className="flex items-center gap-3 border-b border-white/[0.06] bg-[#1c1c1e]/90 px-6 py-3 backdrop-blur-xl">
          <button
            type="button"
            className="flex items-center gap-1.5 text-sm text-white/45 transition-colors hover:text-white/80"
            onClick={onBack}
          >
            <ArrowLeft className="h-4 w-4" />
            {t("nav.back")}
          </button>
          <span className="text-white/20">/</span>
          <h1 className="text-[15px] font-semibold text-white/90">
            {draft.name || t("profileDetail.importedProfile")}
          </h1>
          <div className="ml-auto flex items-center gap-2">
            {!confirmDelete && (
              <Button variant="danger" size="sm" onClick={() => setConfirmDelete(true)}>
                <Trash2 className="mr-1 h-3.5 w-3.5" />
                {t("nav.delete")}
              </Button>
            )}
            {confirmDelete && (
              <>
                <span className="text-sm text-[#ff453a]/80">{t("profileDetail.confirmDelete")}</span>
                <Button
                  variant="danger"
                  size="sm"
                  onClick={async () => {
                    await onDelete(draft.id);
                    onBack();
                  }}
                >
                  {t("profileDetail.yesDelete")}
                </Button>
                <Button variant="ghost" size="sm" onClick={() => setConfirmDelete(false)}>
                  {t("nav.cancel")}
                </Button>
              </>
            )}
            <Button
              size="sm"
              onClick={save}
              disabled={saving || !draft.name.trim()}
            >
              {saving ? t("nav.saving") : t("nav.save")}
            </Button>
          </div>
        </div>

        <div className="flex-1 overflow-y-auto p-6">
          <div className="mx-auto max-w-2xl space-y-4">
            <div className="rounded-xl border border-[#0a84ff]/20 bg-[#0a84ff]/[0.06] px-4 py-3 text-sm text-[#0a84ff]/80">
              {t("profileDetail.importedNotice")}
            </div>
            <div className="rounded-2xl border border-white/[0.07] bg-white/[0.03] p-6 shadow-[inset_0_1px_0_rgba(255,255,255,0.04)]">
              <div>
                <label className="mb-1.5 block text-sm font-medium text-white/60">{t("profileDetail.name")}</label>
                <Input
                  value={draft.name}
                  onChange={(e) => setField("name", e.target.value)}
                  placeholder={t("profileDetail.namePlaceholder")}
                />
              </div>
            </div>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="flex h-screen flex-col bg-[#1c1c1e]">
      {/* Toolbar */}
      <div className="flex items-center gap-3 border-b border-white/[0.06] bg-[#1c1c1e]/90 px-6 py-3 backdrop-blur-xl">
        <button
          type="button"
          className="flex items-center gap-1.5 text-sm text-white/45 transition-colors hover:text-white/80"
          onClick={onBack}
        >
          <ArrowLeft className="h-4 w-4" />
          {t("nav.back")}
        </button>
        <span className="text-white/20">/</span>
        <h1 className="text-[15px] font-semibold text-white/90">
          {isNew ? t("profileDetail.newProfile") : draft.name || t("profileDetail.editProfile")}
        </h1>
        <div className="ml-auto flex items-center gap-2">
          {!isNew && !confirmDelete && (
            <Button variant="danger" size="sm" onClick={() => setConfirmDelete(true)}>
              <Trash2 className="mr-1 h-3.5 w-3.5" />
              {t("nav.delete")}
            </Button>
          )}
          {!isNew && confirmDelete && (
            <>
              <span className="text-sm text-[#ff453a]/80">{t("profileDetail.confirmDelete")}</span>
              <Button
                variant="danger"
                size="sm"
                onClick={async () => {
                  await onDelete(draft.id);
                  onBack();
                }}
              >
                {t("profileDetail.yesDelete")}
              </Button>
              <Button variant="ghost" size="sm" onClick={() => setConfirmDelete(false)}>
                {t("nav.cancel")}
              </Button>
            </>
          )}
          <Button size="sm" onClick={save} disabled={saving || !validation.valid}>
            {saving ? t("nav.saving") : isNew ? t("nav.create") : t("nav.save")}
          </Button>
        </div>
      </div>

      {/* Form */}
      <div className="flex-1 overflow-y-auto p-6">
        <div className="mx-auto max-w-2xl space-y-4">
          {validation.errors.length > 0 && (
            <div className="rounded-xl border border-[#ff453a]/20 bg-[#ff453a]/[0.08] px-4 py-3 text-sm text-[#ff453a]">
              {validation.errors.map((e) => (
                <p key={e}>{e}</p>
              ))}
            </div>
          )}

          <div className="rounded-2xl border border-white/[0.07] bg-white/[0.03] p-6 shadow-[inset_0_1px_0_rgba(255,255,255,0.04)]">
            <p className="mb-5 text-[11px] font-semibold uppercase tracking-widest text-white/30">
              {t("profileDetail.connection")}
            </p>
            <div className="grid grid-cols-2 gap-4">
              <div className="col-span-2">
                <label className="mb-1.5 block text-sm font-medium text-white/60">{t("profileDetail.name")}</label>
                <Input
                  value={draft.name}
                  onChange={(e) => setField("name", e.target.value)}
                  placeholder={t("profileDetail.namePlaceholder")}
                />
              </div>

              <div className="col-span-2">
                <label className="mb-1.5 block text-sm font-medium text-white/60">
                  {t("profileDetail.sshRemote")}{" "}
                  <span className="font-normal text-white/30">{t("profileDetail.sshRemoteHint")}</span>
                </label>
                <Input
                  value={draft.remote}
                  onChange={(e) => setField("remote", e.target.value)}
                  placeholder={t("profileDetail.sshRemotePlaceholder")}
                />
              </div>

              <div className="col-span-2">
                <label className="mb-1.5 block text-sm font-medium text-white/60">
                  {t("profileDetail.identityKey")}
                </label>
                {/* Tab selector */}
                <div className="mb-2 flex gap-1 rounded-lg bg-white/[0.04] p-0.5">
                  <button
                    type="button"
                    className={`flex-1 rounded-md px-3 py-1 text-xs font-medium transition-colors ${
                      !draft.identityKey
                        ? "bg-white/[0.1] text-white/80"
                        : "text-white/40 hover:text-white/60"
                    }`}
                    onClick={() => {
                      setField("identityKey", undefined);
                    }}
                  >
                    {t("profileDetail.filePath")}
                  </button>
                  <button
                    type="button"
                    className={`flex-1 rounded-md px-3 py-1 text-xs font-medium transition-colors ${
                      draft.identityKey !== undefined
                        ? "bg-white/[0.1] text-white/80"
                        : "text-white/40 hover:text-white/60"
                    }`}
                    onClick={() => {
                      setField("identityKey", draft.identityKey ?? "");
                    }}
                  >
                    {t("profileDetail.pemText")}
                  </button>
                </div>
                {draft.identityKey === undefined ? (
                  <div className="flex gap-2">
                    <Input
                      className="flex-1"
                      value={draft.identityFile}
                      onChange={(e) => setField("identityFile", e.target.value)}
                      placeholder={t("profileDetail.identityFilePlaceholder")}
                    />
                    <Button
                      variant="ghost"
                      size="sm"
                      className="shrink-0 px-2"
                      onClick={async () => {
                        const file = await open({
                          title: t("profileDetail.selectSshIdentity"),
                          directory: false,
                          multiple: false,
                        });
                        if (file) setField("identityFile", file);
                      }}
                    >
                      <FolderOpen className="h-4 w-4" />
                    </Button>
                  </div>
                ) : (
                  <Textarea
                    value={draft.identityKey}
                    onChange={(e) => setField("identityKey", e.target.value)}
                    placeholder={"-----BEGIN OPENSSH PRIVATE KEY-----\n...\n-----END OPENSSH PRIVATE KEY-----"}
                    rows={6}
                    className="font-mono text-xs"
                  />
                )}
              </div>

              {/* Jump Hosts */}
              <div className="col-span-2">
                <div className="mb-1.5 flex items-center justify-between">
                  <label className="text-sm font-medium text-white/60">
                    {t("profileDetail.jumpHosts")}{" "}
                    <span className="font-normal text-white/30">{t("profileDetail.proxyJump")}</span>
                  </label>
                  <button
                    type="button"
                    className="flex items-center gap-1 text-xs text-[#0a84ff]/80 transition-colors hover:text-[#0a84ff]"
                    onClick={addJumpHost}
                  >
                    <Plus className="h-3 w-3" /> {t("profileDetail.add")}
                  </button>
                </div>
                {jumpHosts.length === 0 ? (
                  <p className="text-xs text-white/25">{t("profileDetail.noJumpHosts")}</p>
                ) : (
                  <div className="space-y-3">
                    {jumpHosts.map((jh, idx) => (
                      <div
                        key={idx}
                        className="rounded-xl border border-white/[0.06] bg-white/[0.02] p-3"
                      >
                        <div className="mb-2 flex items-center justify-between">
                          <span className="text-[11px] font-medium text-white/30">
                            {t("profileDetail.hop", { num: idx + 1 })}
                          </span>
                          <button
                            type="button"
                            className="text-white/25 transition-colors hover:text-[#ff453a]"
                            onClick={() => removeJumpHost(idx)}
                          >
                            <X className="h-3.5 w-3.5" />
                          </button>
                        </div>
                        <Input
                          value={jh.remote}
                          onChange={(e) => updateJumpHost(idx, { remote: e.target.value })}
                          placeholder={t("profileDetail.jumpHostPlaceholder")}
                          className="mb-2"
                        />
                        {/* Identity tab for this jump host */}
                        <div className="flex gap-1 rounded-lg bg-white/[0.04] p-0.5 mb-2">
                          <button
                            type="button"
                            className={`flex-1 rounded-md px-2 py-0.5 text-[10px] font-medium transition-colors ${
                              jh.identityKey === undefined
                                ? "bg-white/[0.1] text-white/80"
                                : "text-white/40 hover:text-white/60"
                            }`}
                            onClick={() => updateJumpHost(idx, { identityKey: undefined })}
                          >
                            {t("profileDetail.filePath")}
                          </button>
                          <button
                            type="button"
                            className={`flex-1 rounded-md px-2 py-0.5 text-[10px] font-medium transition-colors ${
                              jh.identityKey !== undefined
                                ? "bg-white/[0.1] text-white/80"
                                : "text-white/40 hover:text-white/60"
                            }`}
                            onClick={() => updateJumpHost(idx, { identityKey: jh.identityKey ?? "" })}
                          >
                            {t("profileDetail.pemText")}
                          </button>
                        </div>
                        {jh.identityKey === undefined ? (
                          <div className="flex gap-2">
                            <Input
                              className="flex-1"
                              value={jh.identityFile ?? ""}
                              onChange={(e) =>
                                updateJumpHost(idx, {
                                  identityFile: e.target.value || undefined,
                                })
                              }
                              placeholder={t("profileDetail.jumpHostIdentityPlaceholder")}
                            />
                            <Button
                              variant="ghost"
                              size="sm"
                              className="shrink-0 px-2"
                              onClick={async () => {
                                const file = await open({
                                  title: t("profileDetail.selectSshIdentity"),
                                  directory: false,
                                  multiple: false,
                                });
                                if (file) updateJumpHost(idx, { identityFile: file });
                              }}
                            >
                              <FolderOpen className="h-3.5 w-3.5" />
                            </Button>
                          </div>
                        ) : (
                          <Textarea
                            value={jh.identityKey}
                            onChange={(e) => updateJumpHost(idx, { identityKey: e.target.value })}
                            placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
                            rows={4}
                            className="font-mono text-xs"
                          />
                        )}
                      </div>
                    ))}
                  </div>
                )}
              </div>

              <div className="col-span-2">
                <label className="mb-1.5 block text-sm font-medium text-white/60">
                  {t("profileDetail.includeSubnets")}{" "}
                  <span className="font-normal text-white/30">{t("profileDetail.commaSeparatedCidr")}</span>
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
                  placeholder={t("profileDetail.includeSubnetsPlaceholder")}
                />
              </div>

              <div>
                <label className="mb-1.5 block text-sm font-medium text-white/60">{t("profileDetail.dnsMode")}</label>
                <Select
                  value={draft.dns}
                  onChange={(e) => {
                    const mode = e.target.value as Profile["dns"];
                    setField("dns", mode);
                    if (mode !== "specific") setField("dnsTarget", undefined);
                  }}
                >
                  <option value="off">{t("profileDetail.dnsOff")}</option>
                  <option value="all">{t("profileDetail.dnsAll")}</option>
                  <option value="specific">{t("profileDetail.dnsSpecific")}</option>
                </Select>
              </div>

              {draft.dns === "specific" && (
                <div>
                  <label className="mb-1.5 block text-sm font-medium text-white/60">
                    {t("profileDetail.dnsTarget")}
                  </label>
                  <Input
                    value={draft.dnsTarget ?? ""}
                    onChange={(e) => setField("dnsTarget", e.target.value || undefined)}
                    placeholder={t("profileDetail.dnsTargetPlaceholder")}
                  />
                </div>
              )}
            </div>
          </div>

          {/* Advanced toggle */}
          <button
            className="flex items-center gap-2 text-sm font-medium text-white/35 transition-colors hover:text-white/60"
            type="button"
            onClick={() => setShowAdvanced((s) => !s)}
          >
            <span className="text-[10px]">{showAdvanced ? "▲" : "▼"}</span>
            {showAdvanced ? t("profileDetail.hideAdvanced") : t("profileDetail.showAdvanced")}
          </button>

          {showAdvanced && (
            <div className="rounded-2xl border border-white/[0.07] bg-white/[0.03] p-6 shadow-[inset_0_1px_0_rgba(255,255,255,0.04)]">
              <p className="mb-5 text-[11px] font-semibold uppercase tracking-widest text-white/30">
                {t("profileDetail.advanced")}
              </p>
              <div className="grid grid-cols-2 gap-4">
                <div className="col-span-2">
                  <label className="mb-1.5 block text-sm font-medium text-white/60">
                    {t("profileDetail.excludeSubnets")}{" "}
                    <span className="font-normal text-white/30">{t("profileDetail.commaSeparated")}</span>
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
                  <label className="mb-1.5 block text-sm font-medium text-white/60">{t("profileDetail.method")}</label>
                  <Select value={draft.method} onChange={(e) => setField("method", e.target.value)}>
                    {availableMethods.map((m) => (
                      <option key={m} value={m}>
                        {m}
                      </option>
                    ))}
                  </Select>
                  {methodFeatures && draft.method !== "auto" && methodFeatures[draft.method] && (
                    <p className="mt-1 text-[11px] text-white/25">
                      {t("profileDetail.features", { features: methodFeatures[draft.method].join(", ") || "none" })}
                    </p>
                  )}
                </div>

                <div className="flex flex-col justify-end gap-3">
                  <label className="inline-flex items-center gap-2.5 text-sm text-white/55">
                    <input
                      type="checkbox"
                      checked={draft.autoNets}
                      onChange={(e) => setField("autoNets", e.target.checked)}
                      className="accent-[#0a84ff]"
                    />
                    <span>
                      {t("profileDetail.autoNets")}
                      <span className="ml-1.5 text-xs text-white/30">
                        {t("profileDetail.autoNetsDesc")}
                      </span>
                    </span>
                  </label>
                  {activeFeatures.has("ipv6") && (
                    <label className="inline-flex items-center gap-2.5 text-sm text-white/55">
                      <input
                        type="checkbox"
                        checked={draft.disableIpv6}
                        onChange={(e) => setField("disableIpv6", e.target.checked)}
                        className="accent-[#0a84ff]"
                      />
                      {t("profileDetail.disableIpv6")}
                    </label>
                  )}
                  {activeFeatures.has("blockUdp") && (
                    <label className="inline-flex items-center gap-2.5 text-sm text-white/55">
                      <input
                        type="checkbox"
                        checked={draft.blockUdp}
                        onChange={(e) => setField("blockUdp", e.target.checked)}
                        className="accent-[#0a84ff]"
                      />
                      <span>
                        {t("profileDetail.blockUdp")}
                        <span className="ml-1.5 text-xs text-white/30">{t("profileDetail.blockUdpDesc")}</span>
                      </span>
                    </label>
                  )}
                  {activeFeatures.has("udp") && (
                    <label className="inline-flex items-center gap-2.5 text-sm text-white/55">
                      <input
                        type="checkbox"
                        checked={draft.enableUdp}
                        onChange={(e) => setField("enableUdp", e.target.checked)}
                        className="accent-[#0a84ff]"
                      />
                      <span>
                        {t("profileDetail.udpProxy")}
                        <span className="ml-1.5 text-xs text-white/30">{t("profileDetail.udpProxyDesc")}</span>
                      </span>
                    </label>
                  )}
                </div>

                <div className="col-span-2">
                  <label className="inline-flex items-center gap-2.5 text-sm text-white/55">
                    <input
                      type="checkbox"
                      checked={draft.autoExcludeLan}
                      onChange={(e) => setField("autoExcludeLan", e.target.checked)}
                      className="accent-[#0a84ff]"
                    />
                    <span>
                      {t("profileDetail.autoExcludeLan")}
                      <span className="ml-1.5 text-xs text-white/30">
                        {t("profileDetail.autoExcludeLanDesc")}
                      </span>
                    </span>
                  </label>
                </div>


                <div className="col-span-2">
                  <label className="mb-1.5 block text-sm font-medium text-white/60">
                    {t("profileDetail.sshConnectionPool")}
                    <span className="ml-1.5 font-normal text-white/30">
                      {t("profileDetail.sshConnectionPoolDesc")}
                    </span>
                  </label>
                  <Input
                    type="number"
                    min={1}
                    max={16}
                    value={draft.poolSize}
                    onChange={(e) => setField("poolSize", Math.max(1, parseInt(e.target.value) || 1))}
                  />
                </div>

                {draft.poolSize > 1 && (
                  <div className="col-span-2">
                    <label className="mb-1.5 block text-sm font-medium text-white/60">
                      {t("profileDetail.tcpLoadBalancing")}
                      <span className="ml-1.5 font-normal text-white/30">
                        {t("profileDetail.tcpLoadBalancingDesc")}
                      </span>
                    </label>
                    <select
                      value={draft.tcpBalanceMode ?? "least-loaded"}
                      onChange={(e) => setField("tcpBalanceMode", e.target.value as "round-robin" | "least-loaded")}
                      className="w-full rounded-lg border border-white/[0.08] bg-white/[0.04] px-3 py-2 text-sm text-white/80 focus:border-[#0a84ff]/50 focus:outline-none"
                    >
                      <option value="least-loaded">{t("profileDetail.leastLoaded")}</option>
                      <option value="round-robin">{t("profileDetail.roundRobin")}</option>
                    </select>
                  </div>
                )}

                <div className="col-span-2">
                  <label className="inline-flex items-center gap-2.5 text-sm text-white/55">
                    <input
                      type="checkbox"
                      checked={draft.splitConn}
                      onChange={(e) => setField("splitConn", e.target.checked)}
                      className="accent-[#0a84ff]"
                    />
                    <span>
                      {t("profileDetail.splitConn")}
                      <span className="ml-1.5 text-xs text-white/30">
                        {t("profileDetail.splitConnDesc")}
                      </span>
                    </span>
                  </label>
                </div>

                <div className="col-span-2">
                  <label className="mb-1.5 block text-sm font-medium text-white/60">
                    {t("profileDetail.extraSshOptions")}
                  </label>
                  <Input
                    value={draft.extraSshOptions ?? ""}
                    onChange={(e) => setField("extraSshOptions", e.target.value || undefined)}
                    placeholder={t("profileDetail.extraSshPlaceholder")}
                  />
                </div>

                <div className="col-span-2">
                  <label className="mb-1.5 block text-sm font-medium text-white/60">{t("profileDetail.notes")}</label>
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
