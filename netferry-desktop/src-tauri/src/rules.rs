//! Global routing rules, persisted in `<DataDir>/rules.json`.
//!
//! There is exactly one rule set; it applies whichever profile is connected.
//! Profile groups are only folders for profiles. Older builds stored rules on
//! each `groups/*.json`; when `rules.json` is missing those are merged once
//! (see [`merge_legacy_groups`]). Group files are never rewritten by the merge.

use crate::models::{RouteMode, RuleGroup};
use serde::{Deserialize, Serialize};
use std::collections::{BTreeMap, HashSet};
use std::fs;
use std::path::{Path, PathBuf};
use tauri::{AppHandle, Manager};

pub const KNOWN_HOSTS_CAP: usize = 1000;

fn deserialize_final_route<'de, D>(d: D) -> Result<RouteMode, D::Error>
where
    D: serde::Deserializer<'de>,
{
    Ok(RouteMode::deserialize(d)?.as_final())
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct RuleSet {
    /// Per-host overrides: exact host, IP, or `*.suffix`.
    #[serde(default)]
    pub rules: BTreeMap<String, RouteMode>,
    /// Ordered: the first group with a matching domain wins (Clash semantics).
    #[serde(default)]
    pub rule_groups: Vec<RuleGroup>,
    /// Fallback for traffic matched by neither `rules` nor `rule_groups`.
    /// Only tunnel/direct; blocked is coerced to tunnel on read.
    #[serde(default, deserialize_with = "deserialize_final_route")]
    pub final_route: RouteMode,
    /// Every destination host/IP the relay has observed, oldest first.
    /// Capped at [`KNOWN_HOSTS_CAP`], keeping the newest.
    #[serde(default)]
    pub known_hosts: Vec<String>,
}

impl Default for RuleSet {
    fn default() -> Self {
        Self {
            rules: BTreeMap::new(),
            rule_groups: Vec::new(),
            final_route: RouteMode::tunnel(),
            known_hosts: Vec::new(),
        }
    }
}

impl RuleSet {
    fn normalized(mut self) -> Self {
        self.final_route = self.final_route.as_final();
        cap_known_hosts(&mut self.known_hosts);
        self
    }
}

fn cap_known_hosts(hosts: &mut Vec<String>) {
    if hosts.len() > KNOWN_HOSTS_CAP {
        hosts.drain(..hosts.len() - KNOWN_HOSTS_CAP);
    }
}

// ── Legacy merge (contract §3) ──────────────────────────────────────────────

struct LegacyGroupRules {
    rules: Vec<(String, RouteMode)>,
    rule_groups: Vec<RuleGroup>,
    final_route: Option<RouteMode>,
    known_hosts: Vec<String>,
}

fn extract_legacy(raw: &serde_json::Value) -> LegacyGroupRules {
    let rules = raw
        .get("rules")
        .and_then(|v| v.as_object())
        .map(|m| {
            m.iter()
                .map(|(k, v)| {
                    let mode = RouteMode::deserialize(v).unwrap_or_else(|_| RouteMode::tunnel());
                    (k.clone(), mode)
                })
                .collect()
        })
        .unwrap_or_default();
    let rule_groups = raw
        .get("ruleGroups")
        .and_then(|v| v.as_array())
        .map(|a| {
            a.iter()
                .filter_map(|g| RuleGroup::deserialize(g).ok())
                .collect()
        })
        .unwrap_or_default();
    let final_route = raw
        .get("finalRoute")
        .filter(|v| !v.is_null())
        .map(|v| {
            RouteMode::deserialize(v)
                .unwrap_or_else(|_| RouteMode::tunnel())
                .as_final()
        });
    let known_hosts = raw
        .get("knownHosts")
        .and_then(|v| v.as_array())
        .map(|a| a.iter().filter_map(|h| h.as_str().map(str::to_string)).collect())
        .unwrap_or_default();
    LegacyGroupRules { rules, rule_groups, final_route, known_hosts }
}

/// Merge the rule fields of pre-global group files into one [`RuleSet`].
///
/// `groups` are `(group_id, raw group JSON)`; `active_group_id` is
/// `settings.json`'s `activeGroupId`.
/// - rules: non-active groups in id order, then the active group (wins conflicts)
/// - ruleGroups: active group's first, then the rest by id, skipping duplicate ids
/// - finalRoute: the active group's, else tunnel
/// - knownHosts: non-active (id order) then active, deduped keeping first
///   occurrence, then the last 1000
pub fn merge_legacy_groups(
    groups: Vec<(String, serde_json::Value)>,
    active_group_id: Option<&str>,
) -> RuleSet {
    let mut others: Vec<(String, LegacyGroupRules)> = Vec::new();
    let mut active: Option<LegacyGroupRules> = None;
    for (id, raw) in &groups {
        let parsed = extract_legacy(raw);
        if active.is_none() && Some(id.as_str()) == active_group_id {
            active = Some(parsed);
        } else {
            others.push((id.clone(), parsed));
        }
    }
    others.sort_by(|a, b| a.0.cmp(&b.0));

    let mut out = RuleSet::default();

    for g in others.iter().map(|(_, g)| g).chain(active.iter()) {
        for (host, mode) in &g.rules {
            out.rules.insert(host.clone(), mode.clone());
        }
    }

    let mut seen_ids = HashSet::new();
    for g in active.iter().chain(others.iter().map(|(_, g)| g)) {
        for rg in &g.rule_groups {
            if seen_ids.insert(rg.id.clone()) {
                out.rule_groups.push(rg.clone());
            }
        }
    }

    out.final_route = active
        .as_ref()
        .and_then(|g| g.final_route.clone())
        .unwrap_or_else(RouteMode::tunnel);

    let mut seen_hosts = HashSet::new();
    for g in others.iter().map(|(_, g)| g).chain(active.iter()) {
        for h in &g.known_hosts {
            if seen_hosts.insert(h.clone()) {
                out.known_hosts.push(h.clone());
            }
        }
    }
    cap_known_hosts(&mut out.known_hosts);

    out
}

// ── File IO ─────────────────────────────────────────────────────────────────

fn data_dir(app: &AppHandle) -> Result<PathBuf, String> {
    let dir = app
        .path()
        .app_data_dir()
        .map_err(|e| format!("Failed to read app data directory: {e}"))?;
    if !dir.exists() {
        fs::create_dir_all(&dir)
            .map_err(|e| format!("Failed to create app data directory: {e}"))?;
    }
    Ok(dir)
}

fn rules_path_in(dir: &Path) -> PathBuf {
    dir.join("rules.json")
}

/// Read every `groups/*.json` as raw JSON (before `ProfileGroup` drops the
/// rule fields). The group id is the file's `id` field, else the file stem.
pub fn read_raw_groups(dir: &Path) -> Result<Vec<(String, serde_json::Value)>, String> {
    let groups_dir = dir.join("groups");
    let mut out = Vec::new();
    let entries = match fs::read_dir(&groups_dir) {
        Ok(e) => e,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(out),
        Err(e) => return Err(format!("Failed to read groups directory: {e}")),
    };
    for entry in entries.flatten() {
        let path = entry.path();
        if path.extension().and_then(|e| e.to_str()) != Some("json") {
            continue;
        }
        let raw = fs::read_to_string(&path)
            .map_err(|e| format!("Failed to read {}: {e}", path.display()))?;
        if raw.trim().is_empty() {
            continue;
        }
        let value: serde_json::Value = serde_json::from_str(&raw)
            .map_err(|e| format!("Failed to parse {}: {e}", path.display()))?;
        let id = value
            .get("id")
            .and_then(|v| v.as_str())
            .map(str::to_string)
            .or_else(|| path.file_stem().and_then(|s| s.to_str()).map(str::to_string))
            .unwrap_or_default();
        out.push((id, value));
    }
    Ok(out)
}

fn read_rules_file(path: &Path) -> Result<Option<RuleSet>, String> {
    if !path.exists() {
        return Ok(None);
    }
    let raw = fs::read_to_string(path).map_err(|e| format!("Failed to read rules.json: {e}"))?;
    if raw.trim().is_empty() {
        return Ok(Some(RuleSet::default()));
    }
    let set: RuleSet =
        serde_json::from_str(&raw).map_err(|e| format!("Failed to parse rules.json: {e}"))?;
    Ok(Some(set.normalized()))
}

/// Atomic write: temp file in the same directory, then rename over the target.
fn write_rules_file(path: &Path, set: &RuleSet) -> Result<(), String> {
    let set = set.clone().normalized();
    let json = serde_json::to_string_pretty(&set)
        .map_err(|e| format!("Failed to serialize rules: {e}"))?;
    let tmp = path.with_extension("json.tmp");
    fs::write(&tmp, json).map_err(|e| format!("Failed to write {}: {e}", tmp.display()))?;
    fs::rename(&tmp, path).map_err(|e| {
        let _ = fs::remove_file(&tmp);
        format!("Failed to replace {}: {e}", path.display())
    })
}

/// Load `rules.json` from `dir`, migrating from `groups/*.json` (plus any
/// `extra_groups`, which take part in the merge as if they were files) when it
/// doesn't exist yet.
pub fn load_rules_in(
    dir: &Path,
    active_group_id: Option<&str>,
    extra_groups: Vec<(String, serde_json::Value)>,
) -> Result<RuleSet, String> {
    let path = rules_path_in(dir);
    if let Some(set) = read_rules_file(&path)? {
        return Ok(set);
    }
    let mut groups = read_raw_groups(dir)?;
    groups.extend(extra_groups);
    let set = merge_legacy_groups(groups, active_group_id);
    write_rules_file(&path, &set)?;
    log::info!(
        "rules: migrated rules.json ({} rules, {} rule groups, {} known hosts)",
        set.rules.len(),
        set.rule_groups.len(),
        set.known_hosts.len(),
    );
    Ok(set)
}

/// Like [`load_rules`], but lets the caller inject additional raw groups into
/// the one-time merge (used by `migrate_v2` for the not-yet-written default
/// group carrying legacy `routes.json` entries).
pub fn load_rules_with_extra(
    app: &AppHandle,
    extra_groups: Vec<(String, serde_json::Value)>,
    active_group_id: Option<&str>,
) -> Result<RuleSet, String> {
    load_rules_in(&data_dir(app)?, active_group_id, extra_groups)
}

/// Load the global rule set, migrating from group files on first use.
pub fn load_rules(app: &AppHandle) -> Result<RuleSet, String> {
    let dir = data_dir(app)?;
    if let Some(set) = read_rules_file(&rules_path_in(&dir))? {
        return Ok(set);
    }
    let active = crate::settings::load_settings(app)
        .unwrap_or_default()
        .active_group_id;
    load_rules_in(&dir, active.as_deref(), Vec::new())
}

/// Make sure `rules.json` exists before anything rewrites or deletes a group
/// file (which would drop that group's legacy rule fields).
pub fn ensure_migrated(app: &AppHandle) -> Result<(), String> {
    load_rules(app).map(|_| ())
}

pub fn save_rules(app: &AppHandle, set: &RuleSet) -> Result<(), String> {
    write_rules_file(&rules_path_in(&data_dir(app)?), set)
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    fn g(id: &str, v: serde_json::Value) -> (String, serde_json::Value) {
        (id.to_string(), v)
    }

    fn rm(kind: &str) -> RouteMode {
        RouteMode::from_kind(kind)
    }

    #[test]
    fn active_group_wins_rule_conflicts() {
        let set = merge_legacy_groups(
            vec![
                g("b", json!({"rules": {"x.com": "blocked", "b.com": "direct"}})),
                g("a", json!({"rules": {"x.com": "tunnel", "a.com": "direct"}})),
                g("c", json!({"rules": {"x.com": "direct", "b.com": "tunnel"}})),
            ],
            Some("a"),
        );
        assert_eq!(set.rules["x.com"], rm("tunnel"), "active a wins");
        assert_eq!(set.rules["b.com"], rm("tunnel"), "c overrides b (id order)");
        assert_eq!(set.rules["a.com"], rm("direct"));
        assert_eq!(set.rules.len(), 3);
    }

    #[test]
    fn rule_groups_active_first_then_by_id_deduped() {
        let rgj = |id: &str| json!({"id": id, "name": id, "domains": [], "route": "direct"});
        let set = merge_legacy_groups(
            vec![
                g("z", json!({"ruleGroups": [rgj("z1"), rgj("shared")]})),
                g("m", json!({"ruleGroups": [rgj("m2"), rgj("m1")]})),
                g("b", json!({"ruleGroups": [rgj("b1"), rgj("shared"), {"bad": 1}]})),
            ],
            Some("m"),
        );
        let ids: Vec<&str> = set.rule_groups.iter().map(|r| r.id.as_str()).collect();
        assert_eq!(ids, ["m2", "m1", "b1", "shared", "z1"]);
    }

    #[test]
    fn final_route_from_active_only() {
        let set = merge_legacy_groups(
            vec![
                g("a", json!({"finalRoute": {"kind": "direct"}})),
                g("b", json!({})),
            ],
            Some("b"),
        );
        assert_eq!(set.final_route, rm("tunnel"));
        let set = merge_legacy_groups(
            vec![g("a", json!({"finalRoute": {"kind": "direct"}})), g("b", json!({}))],
            Some("a"),
        );
        assert_eq!(set.final_route, rm("direct"));
        let set = merge_legacy_groups(vec![g("a", json!({"finalRoute": "blocked"}))], Some("a"));
        assert_eq!(set.final_route, rm("tunnel"), "blocked final coerced");
        let set = merge_legacy_groups(vec![g("a", json!({"finalRoute": "direct"}))], None);
        assert_eq!(set.final_route, rm("tunnel"), "no active group");
    }

    #[test]
    fn known_hosts_order_dedupe_cap() {
        let set = merge_legacy_groups(
            vec![
                g("act", json!({"knownHosts": ["x", "a", "y"]})),
                g("c", json!({"knownHosts": ["c", "a"]})),
                g("b", json!({"knownHosts": ["b", "a", 5]})),
            ],
            Some("act"),
        );
        assert_eq!(set.known_hosts, ["b", "a", "c", "x", "y"]);

        let old: Vec<String> = (0..800).map(|i| format!("o{i}")).collect();
        let new: Vec<String> = (0..800).map(|i| format!("n{i}")).collect();
        let set = merge_legacy_groups(
            vec![g("act", json!({"knownHosts": new})), g("old", json!({"knownHosts": old}))],
            Some("act"),
        );
        assert_eq!(set.known_hosts.len(), KNOWN_HOSTS_CAP);
        assert_eq!(set.known_hosts.first().unwrap(), "o600");
        assert_eq!(set.known_hosts.last().unwrap(), "n799");
    }

    #[test]
    fn no_groups_gives_empty_tunnel() {
        let set = merge_legacy_groups(Vec::new(), Some("default"));
        assert_eq!(set, RuleSet::default());
        assert_eq!(set.final_route, rm("tunnel"));
        assert_eq!(
            serde_json::to_value(&set).unwrap(),
            json!({"rules": {}, "ruleGroups": [], "finalRoute": {"kind": "tunnel"}, "knownHosts": []}),
            "all four fields always written"
        );
    }

    #[test]
    fn legacy_route_values_normalized() {
        let set = merge_legacy_groups(
            vec![g(
                "a",
                json!({
                    "rules": {
                        "d.com": "DIRECT",
                        "p.com": {"kind": "tunnel", "profileId": "p1"},
                        "o.com": "default",
                        "n.com": null,
                        "k.com": {"kind": "blocked"},
                    },
                    "ruleGroups": [{"id": "g", "name": "G", "route": "weird"}],
                }),
            )],
            Some("a"),
        );
        assert_eq!(set.rules["d.com"], rm("direct"));
        assert_eq!(set.rules["p.com"], rm("tunnel"));
        assert_eq!(set.rules["o.com"], rm("tunnel"));
        assert_eq!(set.rules["n.com"], rm("tunnel"));
        assert_eq!(set.rules["k.com"], rm("blocked"));
        assert_eq!(set.rule_groups[0].route, rm("tunnel"));
        assert!(set.rule_groups[0].domains.is_empty());
    }

    fn temp_dir(tag: &str) -> PathBuf {
        let d = std::env::temp_dir().join(format!(
            "netferry-rules-test-{tag}-{}-{}",
            std::process::id(),
            crate::models::now_ms()
        ));
        fs::create_dir_all(d.join("groups")).unwrap();
        d
    }

    #[test]
    fn load_migrates_once_and_leaves_group_files() {
        let dir = temp_dir("migrate");
        let group_json = r#"{"id":"g1","name":"G1","childrenIds":[],"rules":{"a.com":"direct"},"knownHosts":["h"]}"#;
        fs::write(dir.join("groups/g1.json"), group_json).unwrap();

        let set = load_rules_in(&dir, Some("g1"), Vec::new()).unwrap();
        assert_eq!(set.rules["a.com"], rm("direct"));
        assert_eq!(set.known_hosts, ["h"]);
        assert!(dir.join("rules.json").exists());
        assert!(!dir.join("rules.json.tmp").exists());
        assert_eq!(fs::read_to_string(dir.join("groups/g1.json")).unwrap(), group_json);

        // rules.json now authoritative: later group edits don't re-merge.
        fs::write(dir.join("groups/g1.json"), r#"{"id":"g1","name":"G1","rules":{"b.com":"direct"}}"#)
            .unwrap();
        let again = load_rules_in(&dir, Some("g1"), Vec::new()).unwrap();
        assert_eq!(again, set);

        // Saves round-trip and cap known hosts.
        let mut edited = again.clone();
        edited.known_hosts = (0..1005).map(|i| format!("h{i}")).collect();
        write_rules_file(&rules_path_in(&dir), &edited).unwrap();
        let back = read_rules_file(&rules_path_in(&dir)).unwrap().unwrap();
        assert_eq!(back.known_hosts.len(), KNOWN_HOSTS_CAP);
        assert_eq!(back.known_hosts[0], "h5");

        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn load_without_groups_writes_empty_rules() {
        let dir = temp_dir("empty");
        let set = load_rules_in(&dir, None, Vec::new()).unwrap();
        assert_eq!(set, RuleSet::default());
        let on_disk: serde_json::Value =
            serde_json::from_str(&fs::read_to_string(dir.join("rules.json")).unwrap()).unwrap();
        assert_eq!(on_disk["finalRoute"], json!({"kind": "tunnel"}));
        let _ = fs::remove_dir_all(&dir);
    }
}
