import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { listen, type UnlistenFn } from "@tauri-apps/api/event";
import { Play, Square } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { useProfileStore } from "@/stores/profileStore";
import {
  cancelTraceroute,
  startTraceroute,
  type Hop,
  type TracerouteDone,
} from "@/api";

const HOP_EVENT = "traceroute-hop";
const DONE_EVENT = "traceroute-done";

const GEO_SOURCES = ["LeoMoeAPI", "IPInfo", "IP-API", "IP.SB", "Ip2region"] as const;
type GeoSource = (typeof GEO_SOURCES)[number];

function formatLocation(h: Hop): string {
  const geo = [h.country, h.province, h.city].filter(Boolean).join(" ");
  const owner = h.isp || h.owner;
  if (geo && owner) return `${geo} · ${owner}`;
  return geo || owner || "";
}

function formatRtt(ms: number | undefined): string {
  if (ms === undefined || Number.isNaN(ms)) return "—";
  if (ms < 10) return `${ms.toFixed(2)} ms`;
  if (ms < 100) return `${ms.toFixed(1)} ms`;
  return `${Math.round(ms)} ms`;
}

export function DiagnosticsPage() {
  const { t } = useTranslation();
  const { profiles, loadProfiles } = useProfileStore();

  const [target, setTarget] = useState("");
  const [profileId, setProfileId] = useState<string>("");
  const [geoSource, setGeoSource] = useState<GeoSource>("LeoMoeAPI");
  const [maxHops, setMaxHops] = useState(30);
  const [queries, setQueries] = useState(1);

  const [running, setRunning] = useState(false);
  const [hops, setHops] = useState<Hop[]>([]);
  const [statusMsg, setStatusMsg] = useState<string>("");
  const [errorMsg, setErrorMsg] = useState<string>("");

  const sessionIdRef = useRef<string | null>(null);

  useEffect(() => {
    if (profiles.length === 0) loadProfiles().catch(() => {});
  }, [profiles.length, loadProfiles]);

  // Subscribe to streaming hop / done events for the active session.
  useEffect(() => {
    let unlistenHop: UnlistenFn | null = null;
    let unlistenDone: UnlistenFn | null = null;
    listen<Hop>(HOP_EVENT, (e) => {
      if (e.payload.sessionId !== sessionIdRef.current) return;
      setHops((prev) => {
        // Replace existing TTL row (handles -q > 1 redraws), else append.
        const idx = prev.findIndex((h) => h.ttl === e.payload.ttl);
        if (idx === -1) return [...prev, e.payload];
        const next = prev.slice();
        next[idx] = e.payload;
        return next;
      });
    }).then((fn) => { unlistenHop = fn; });

    listen<TracerouteDone>(DONE_EVENT, (e) => {
      if (e.payload.sessionId !== sessionIdRef.current) return;
      sessionIdRef.current = null;
      setRunning(false);
      setStatusMsg(
        e.payload.exitCode == null || e.payload.exitCode === 0
          ? t("diagnostics.exitedOk")
          : t("diagnostics.exited", { code: e.payload.exitCode }),
      );
    }).then((fn) => { unlistenDone = fn; });

    return () => {
      unlistenHop?.();
      unlistenDone?.();
    };
  }, [t]);

  const handleFromProfile = (id: string) => {
    setProfileId(id);
    if (!id) return;
    const p = profiles.find((x) => x.id === id);
    if (p) setTarget(p.remote);
  };

  const handleStart = async () => {
    if (running) return;
    const trimmed = target.trim();
    if (!trimmed) return;
    setHops([]);
    setStatusMsg("");
    setErrorMsg("");
    try {
      const sid = await startTraceroute({
        target: trimmed,
        maxHops,
        queries,
        geoSource,
      });
      sessionIdRef.current = sid;
      setRunning(true);
    } catch (e) {
      setErrorMsg(t("diagnostics.errorPrefix", { msg: String(e) }));
    }
  };

  const handleStop = async () => {
    const sid = sessionIdRef.current;
    if (!sid) return;
    try {
      await cancelTraceroute(sid);
    } catch {
      // best-effort; the reaper thread will still emit DONE_EVENT.
    }
  };

  return (
    <div className="flex h-full flex-col">
      <div className="flex h-[52px] items-center gap-3 px-6">
        <h1 className="text-[15px] font-semibold text-t1">{t("diagnostics.title")}</h1>
        {running && (
          <span className="text-xs text-t3">{t("diagnostics.running")}</span>
        )}
      </div>

      <div className="flex-1 overflow-y-auto px-6 pb-6">
        <p className="mb-4 text-xs text-t3">{t("diagnostics.subtitle")}</p>

        {/* Controls card */}
        <div className="mb-4 rounded-2xl border border-sep bg-ov-3 p-4">
          <div className="grid gap-3 md:grid-cols-[1fr_220px]">
            <div>
              <label className="mb-1.5 block text-xs font-medium text-t2">
                {t("diagnostics.target")}
              </label>
              <Input
                value={target}
                placeholder={t("diagnostics.targetPlaceholder")}
                onChange={(e) => setTarget(e.target.value)}
                disabled={running}
                onKeyDown={(e) => {
                  if (e.key === "Enter" && !running) handleStart();
                }}
              />
            </div>
            <div>
              <label className="mb-1.5 block text-xs font-medium text-t2">
                {t("diagnostics.fromProfile")}
              </label>
              <Select
                value={profileId}
                onChange={(e) => handleFromProfile(e.target.value)}
                disabled={running}
              >
                <option value="">{t("diagnostics.noProfile")}</option>
                {profiles.map((p) => (
                  <option key={p.id} value={p.id}>{p.name} — {p.remote}</option>
                ))}
              </Select>
            </div>
          </div>

          <div className="mt-3 grid gap-3 md:grid-cols-3">
            <div>
              <label className="mb-1.5 block text-xs font-medium text-t2">
                {t("diagnostics.geoSource")}
              </label>
              <Select
                value={geoSource}
                onChange={(e) => setGeoSource(e.target.value as GeoSource)}
                disabled={running}
              >
                {GEO_SOURCES.map((g) => (
                  <option key={g} value={g}>{g}</option>
                ))}
              </Select>
            </div>
            <div>
              <label className="mb-1.5 block text-xs font-medium text-t2">
                {t("diagnostics.maxHops")}
              </label>
              <Input
                type="number"
                min={1}
                max={64}
                value={maxHops}
                onChange={(e) => setMaxHops(Math.max(1, Math.min(64, Number(e.target.value) || 30)))}
                disabled={running}
              />
            </div>
            <div>
              <label className="mb-1.5 block text-xs font-medium text-t2">
                {t("diagnostics.queries")}
              </label>
              <Input
                type="number"
                min={1}
                max={5}
                value={queries}
                onChange={(e) => setQueries(Math.max(1, Math.min(5, Number(e.target.value) || 1)))}
                disabled={running}
              />
            </div>
          </div>

          <div className="mt-4 flex items-center gap-2">
            {running ? (
              <Button variant="danger" onClick={handleStop}>
                <Square size={14} className="mr-1.5" />
                {t("diagnostics.stop")}
              </Button>
            ) : (
              <Button onClick={handleStart} disabled={!target.trim()}>
                <Play size={14} className="mr-1.5" />
                {t("diagnostics.start")}
              </Button>
            )}
            {statusMsg && <span className="text-xs text-t3">{statusMsg}</span>}
            {errorMsg && <span className="text-xs text-danger">{errorMsg}</span>}
          </div>
        </div>

        {/* Results table */}
        <div className="rounded-2xl border border-sep bg-ov-3 overflow-hidden">
          {hops.length === 0 ? (
            <div className="px-6 py-12 text-center text-sm text-t4">
              {t("diagnostics.empty")}
            </div>
          ) : (
            <table className="w-full text-sm">
              <thead className="bg-ov-6 text-[11px] uppercase tracking-wider text-t3">
                <tr>
                  <th className="px-3 py-2 text-left w-10">{t("diagnostics.col.ttl")}</th>
                  <th className="px-3 py-2 text-left">{t("diagnostics.col.ip")}</th>
                  <th className="px-3 py-2 text-right w-24">{t("diagnostics.col.rtt")}</th>
                  <th className="px-3 py-2 text-left w-24">{t("diagnostics.col.asn")}</th>
                  <th className="px-3 py-2 text-left">{t("diagnostics.col.location")}</th>
                </tr>
              </thead>
              <tbody>
                {[...hops].sort((a, b) => a.ttl - b.ttl).map((h) => (
                  <tr key={h.ttl} className="border-t border-sep">
                    <td className="px-3 py-2 text-t3">{h.ttl}</td>
                    <td className="px-3 py-2 font-mono text-[12px] text-t1">
                      {h.timeout ? (
                        <span className="text-t4">{t("diagnostics.timeoutCell")}</span>
                      ) : (
                        <>
                          <div>{h.ip || "—"}</div>
                          {h.hostname && (
                            <div className="text-[11px] text-t4">{h.hostname}</div>
                          )}
                        </>
                      )}
                    </td>
                    <td className="px-3 py-2 text-right font-mono text-[12px] text-t2">
                      {h.timeout ? "—" : formatRtt(h.rttMs)}
                    </td>
                    <td className="px-3 py-2 font-mono text-[12px] text-t2">
                      {h.asn ? `AS${h.asn}` : ""}
                    </td>
                    <td className="px-3 py-2 text-[12px] text-t2">
                      {formatLocation(h)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </div>
    </div>
  );
}
