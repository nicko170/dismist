import { api } from "./api";
import { useEffect, useMemo, useState, type FormEvent } from "react";
import configFields from "./configFields.json";

type Scope = "site" | "ap" | "wlan";
type Props = {
  siteID: string;
  apID?: string;
  networkID?: string | null;
  onApplied: () => void | Promise<void>;
};

const requestJSON = (path: string, options?: RequestInit) =>
  api<Record<string, unknown>>(path, options);

export default function AdvancedEditor({
  siteID,
  apID,
  networkID,
  onApplied,
}: Props) {
  const [scope, setScope] = useState<Scope>("site");
  const [patch, setPatch] = useState("{}");
  const [preview, setPreview] = useState<Record<string, unknown> | null>(null);
  const [search, setSearch] = useState("");
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => setScope("site"), [siteID]);

  useEffect(() => {
    if (!siteID) return;
    let cancelled = false;
    const load = async () => {
      try {
        let value: unknown = {};
        if (scope === "site") {
          const result = await requestJSON(`/api/sites/${siteID}`);
          value = (result.site as Record<string, unknown>)?.advanced ?? {};
        } else if (scope === "ap" && apID) {
          const result = await requestJSON(
            `/api/sites/${siteID}/aps/${apID}/config`,
          );
          value = result.ap_patch ?? {};
        } else if (scope === "wlan" && networkID) {
          const result = await requestJSON(
            `/api/sites/${siteID}/networks/${networkID}/advanced`,
          );
          value = result.patch ?? {};
        }
        if (!cancelled) {
          setPatch(JSON.stringify(value, null, 2));
          setError("");
          setNotice("");
        }
      } catch (cause) {
        if (!cancelled)
          setError(
            cause instanceof Error
              ? cause.message
              : "Could not load advanced configuration",
          );
      }
    };
    void load();
    return () => {
      cancelled = true;
    };
  }, [siteID, apID, networkID, scope]);

  useEffect(() => {
    if (!siteID || !apID) {
      setPreview(null);
      return;
    }
    let cancelled = false;
    void requestJSON(`/api/sites/${siteID}/aps/${apID}/config`)
      .then((result) => {
        if (!cancelled) setPreview(result.effective as Record<string, unknown>);
      })
      .catch(() => {
        if (!cancelled) setPreview(null);
      });
    return () => {
      cancelled = true;
    };
  }, [siteID, apID]);

  const filteredFields = useMemo(
    () =>
      configFields.filter((field) =>
        field.path.toLowerCase().includes(search.toLowerCase().trim()),
      ),
    [search],
  );

  async function save(event: FormEvent) {
    event.preventDefault();
    setError("");
    setNotice("");
    let parsed: unknown;
    try {
      parsed = JSON.parse(patch);
    } catch {
      setError("Enter a valid JSON object.");
      return;
    }
    if (!parsed || Array.isArray(parsed) || typeof parsed !== "object") {
      setError("The patch must be a JSON object.");
      return;
    }
    const path =
      scope === "site"
        ? `/api/sites/${siteID}/advanced`
        : scope === "ap"
          ? `/api/sites/${siteID}/aps/${apID}/advanced`
          : `/api/sites/${siteID}/networks/${networkID}/advanced`;
    setBusy(true);
    try {
      await requestJSON(path, {
        method: "PUT",
        body: JSON.stringify({ patch: parsed }),
      });
      setNotice("Advanced configuration saved and sent to affected APs.");
      if (apID) {
        const result = await requestJSON(
          `/api/sites/${siteID}/aps/${apID}/config`,
        );
        setPreview(result.effective as Record<string, unknown>);
      }
      await onApplied();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Could not save patch");
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="panel advanced-panel">
      <div className="panel-heading">
        <div>
          <span className="section-kicker">Configuration overrides</span>
          <h3>Advanced configuration</h3>
        </div>
        <span className="count-badge">
          {configFields.length} mapped fields
        </span>
      </div>
      <div className="advanced-intro">
        Edit a JSON patch at the site, AP, or WLAN level. Unknown fields are
        preserved. SSID, VLAN, passphrase, AP identity, and revision stay
        controlled by their dedicated fields.
      </div>
      <div className="advanced-grid">
        <form onSubmit={(event) => void save(event)}>
          <div className="advanced-tabs">
            <button
              type="button"
              className={scope === "site" ? "selected" : ""}
              onClick={() => setScope("site")}
            >
              Site
            </button>
            <button
              type="button"
              disabled={!apID}
              className={scope === "ap" ? "selected" : ""}
              onClick={() => setScope("ap")}
            >
              AP
            </button>
            <button
              type="button"
              disabled={!networkID}
              className={scope === "wlan" ? "selected" : ""}
              onClick={() => setScope("wlan")}
            >
              WLAN
            </button>
          </div>
          <label className="advanced-label" htmlFor="advanced-json">
            {scope.toUpperCase()} JSON patch
          </label>
          <textarea
            id="advanced-json"
            spellCheck={false}
            value={patch}
            onChange={(event) => setPatch(event.target.value)}
          />
          {error && (
            <div className="alert error" role="alert">
              {error}
            </div>
          )}
          {notice && (
            <div className="alert success" role="status">
              {notice}
            </div>
          )}
          <button className="primary-button" disabled={busy} type="submit">
            {busy ? "Saving…" : "Save advanced config"}
          </button>
        </form>
        <div className="field-reference">
          <label className="advanced-label" htmlFor="field-search">
            Config field reference
          </label>
          <input
            id="field-search"
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            placeholder="Search fields…"
          />
          <p className="muted">
            BLE and MQTT paths from 0.15 firmware type metadata show parser
            fields; most runtime behavior is still unverified.
          </p>
          <div className="field-list">
            {filteredFields.slice(0, 70).map((field) => (
              <div key={field.path}>
                <code>{field.path}</code>
                <span>
                  {field.type}
                  {"source" in field ? " · 0.15 type" : ""}
                </span>
              </div>
            ))}
          </div>
          {filteredFields.length > 70 && (
            <p className="muted">
              Showing 70 of {filteredFields.length}; search to narrow the list.
            </p>
          )}
        </div>
      </div>
      <details className="effective-preview">
        <summary>
          Effective AP config preview{" "}
          {apID ? "(secrets redacted)" : "(select an AP)"}
        </summary>
        <pre>
          {preview
            ? JSON.stringify(preview, null, 2)
            : "No AP is assigned to this site yet."}
        </pre>
      </details>
    </section>
  );
}
