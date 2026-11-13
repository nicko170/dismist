import { api } from "./api";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";

type Sample = {
  minute: string;
  connected: boolean;
  client_count: number;
  average_rssi: number;
  rx_bytes: number;
  tx_bytes: number;
};

type Session = {
  mac: string;
  name?: string;
  ip?: string;
  ssid: string;
  vlan_id: number;
  connected_at: string;
  disconnected_at?: string;
  end_reason?: string;
};

type Props = { siteID: string; apID?: string; refreshSignal?: number };

function shortTime(value: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime())
    ? "Unknown"
    : date.toLocaleTimeString(undefined, {
        hour: "2-digit",
        minute: "2-digit",
      });
}

export default function HistoryPanel({
  siteID,
  apID,
  refreshSignal = 0,
}: Props) {
  const [samples, setSamples] = useState<Sample[]>([]);
  const [sessions, setSessions] = useState<Session[]>([]);
  const [error, setError] = useState("");
  const requestID = useRef(0);
  const lastLoadAt = useRef(0);
  const pendingRefresh = useRef<number | undefined>(undefined);

  const load = useCallback(async () => {
    if (!siteID || !apID || Date.now() - lastLoadAt.current < 5000) return;
    lastLoadAt.current = Date.now();
    const currentRequest = ++requestID.current;
    try {
      const base = `/api/sites/${encodeURIComponent(siteID)}/aps/${encodeURIComponent(apID)}`;
      const [sampleData, sessionData] = await Promise.all([
        api<{ samples: Sample[] }>(`${base}/samples?limit=120`),
        api<{ sessions: Session[] }>(`${base}/sessions?limit=20`),
      ]);
      if (currentRequest === requestID.current) {
        setSamples(sampleData.samples || []);
        setSessions(sessionData.sessions || []);
        setError("");
      }
    } catch (cause) {
      if (currentRequest === requestID.current)
        setError(
          cause instanceof Error ? cause.message : "History unavailable",
        );
    }
  }, [siteID, apID]);

  useEffect(() => {
    requestID.current++;
    lastLoadAt.current = 0;
    if (pendingRefresh.current !== undefined) window.clearTimeout(pendingRefresh.current);
    pendingRefresh.current = undefined;
    setSamples([]);
    setSessions([]);
    setError("");
    if (!siteID || !apID) return;
    void load();
    const timer = window.setInterval(() => void load(), 15000);
    return () => {
      requestID.current++;
      window.clearInterval(timer);
      if (pendingRefresh.current !== undefined) window.clearTimeout(pendingRefresh.current);
      pendingRefresh.current = undefined;
    };
  }, [siteID, apID, load]);

  useEffect(() => {
    if (!refreshSignal || !siteID || !apID || pendingRefresh.current !== undefined) return;
    const remaining = 5000 - (Date.now() - lastLoadAt.current);
    if (remaining <= 0) {
      void load();
    } else {
      pendingRefresh.current = window.setTimeout(() => {
        pendingRefresh.current = undefined;
        void load();
      }, remaining + 10);
    }
  }, [refreshSignal, siteID, apID, load]);

  const knownCounts = samples
    .filter((sample) => sample.client_count >= 0)
    .map((sample) => sample.client_count);
  const peakClients = Math.max(0, ...knownCounts);
  const chartScale = Math.max(1, peakClients);
  const path = useMemo(() => {
    if (!knownCounts.length) return "";
    return samples
      .map((sample, index) => {
        if (sample.client_count < 0) return "";
        const x =
          samples.length === 1
            ? 290
            : 16 + (index / (samples.length - 1)) * 548;
        const y = 122 - (sample.client_count / chartScale) * 100;
        const previousKnown = index > 0 && samples[index - 1].client_count >= 0;
        return `${previousKnown ? "L" : "M"}${x.toFixed(1)} ${y.toFixed(1)}`;
      })
      .join(" ");
  }, [samples, chartScale, knownCounts.length]);

  return (
    <section className="panel history-panel">
      <div className="panel-heading">
        <div>
          <span className="section-kicker">Stored in bbolt</span>
          <h3>Client history</h3>
        </div>
        <span className="count-badge">{samples.length} minute samples</span>
      </div>
      {error && (
        <p className="history-error" role="alert">
          {error}
        </p>
      )}
      {!apID ? (
        <p className="empty-activity">
          Assign an AP to this site to start history.
        </p>
      ) : (
        <div className="history-grid">
          <div className="history-chart">
            <div className="history-chart-title">
              <strong>Associated clients</strong>
              <span>last {samples.length} sampled minutes</span>
            </div>
            {knownCounts.length ? (
              <>
                <svg
                  viewBox="0 0 580 140"
                  role="img"
                  aria-label="Associated clients over time"
                >
                  <line x1="16" y1="122" x2="564" y2="122" className="axis" />
                  <line
                    x1="16"
                    y1="72"
                    x2="564"
                    y2="72"
                    className="grid-line"
                  />
                  <line
                    x1="16"
                    y1="22"
                    x2="564"
                    y2="22"
                    className="grid-line"
                  />
                  <path d={path} className="chart-line" />
                </svg>
                <div className="chart-range">
                  <span>{shortTime(samples[0].minute)}</span>
                  <span>Peak {peakClients}</span>
                  <span>{shortTime(samples[samples.length - 1].minute)}</span>
                </div>
              </>
            ) : samples.length ? (
              <p className="muted">
                Client counts are unavailable for the sampled period.
              </p>
            ) : (
              <p className="muted">
                Minute samples will appear after the AP reports telemetry.
              </p>
            )}
          </div>
          <div className="history-sessions">
            <h4>Recent client sessions</h4>
            {sessions.length ? (
              [...sessions]
                .reverse()
                .slice(0, 5)
                .map((session, index) => (
                  <div
                    className="session-row"
                    key={`${session.mac}-${session.connected_at}-${index}`}
                  >
                    <div>
                      <strong>{session.name || session.mac}</strong>
                      <span>
                        {session.ip || session.mac} · {session.ssid} · VLAN{" "}
                        {session.vlan_id}
                      </span>
                    </div>
                    <span
                      className={
                        session.disconnected_at
                          ? "session-ended"
                          : "session-active"
                      }
                    >
                      {session.disconnected_at ? "Ended" : "Current"}
                    </span>
                  </div>
                ))
            ) : (
              <p className="muted">No client sessions recorded yet.</p>
            )}
          </div>
        </div>
      )}
    </section>
  );
}
