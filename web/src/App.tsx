import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type FormEvent,
} from "react";
import {
  Activity,
  CircleHelp,
  LayoutDashboard,
  ListFilter,
  LogOut,
  Radio,
  RefreshCw,
  Router,
  Settings2,
  ShieldCheck,
  Users,
  Wifi,
} from "lucide-react";
import {
  api,
  bytesLabel,
  durationLabel,
  timeLabel,
  UnauthorizedError,
} from "./api";
import AdvancedEditor from "./AdvancedEditor";
import APEditor from "./APEditor";
import APInventory, { apStatus, type APEntry } from "./APInventory";
import HistoryPanel from "./HistoryPanel";
import SiteBar, { type Site } from "./SiteBar";
import { Badge } from "./components/ui/badge";
import { Button } from "./components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "./components/ui/card";
import { Input } from "./components/ui/input";
import { Label } from "./components/ui/label";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "./components/ui/table";
import { writeOnlySecret } from "./lib/utils";

type Page = "overview" | "aps" | "clients" | "wlans" | "events" | "settings";
type Network = {
  id: string;
  ssid: string;
  vlan_id: number;
  enabled: boolean;
  has_psk: boolean;
};
type Status = {
  ap_id: string;
  desired_revision: number;
  ap: {
    connected: boolean;
    firmware: string;
    last_seen: string;
    applied_revision: number;
    last_error?: string;
  };
};
type VAP = { interface: string; ssid: string; bssid: string };
type RadioData = { interface: string; width_mhz: number };
type Telemetry = {
  full_frames: number;
  fast_frames: number;
  last_full: string;
  last_fast: string;
  uptime_seconds: number;
  vaps: VAP[] | null;
  radios: RadioData[] | null;
};
type Client = {
  mac: string;
  name?: string;
  ip?: string;
  ssid: string;
  bssid: string;
  vlan_id: number;
  rssi: number;
  connected_seconds: number;
  tx_bytes: number;
  rx_bytes: number;
  tx_mbps: number;
  rx_mbps: number;
  auth?: string;
  cipher?: string;
  protocol?: string;
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
  rssi?: number;
  tx_bytes?: number;
  rx_bytes?: number;
  ap_id: string;
};
type EventRecord = {
  when: string;
  kind: string;
  title: string;
  detail?: string;
  client_mac?: string;
  ap_id?: string;
};
type APData = {
  status: Status | null;
  telemetry: Telemetry | null;
  clients: Client[];
  sessions: Session[];
  events: EventRecord[];
};
type ClientRow = {
  mac: string;
  name?: string;
  ip?: string;
  ssid: string;
  vlan_id: number;
  rssi?: number;
  ap_id: string;
  online: boolean;
  tx_bytes?: number;
  rx_bytes?: number;
  connected_seconds?: number;
  connected_at?: string;
  disconnected_at?: string;
  protocol?: string;
  auth?: string;
};
type FormState = { ssid: string; vlan: string; psk: string; enabled: boolean };

const nav: { page: Page; label: string; icon: typeof LayoutDashboard }[] = [
  { page: "overview", label: "Overview", icon: LayoutDashboard },
  { page: "aps", label: "Access points", icon: Router },
  { page: "clients", label: "Clients", icon: Users },
  { page: "wlans", label: "WLANs", icon: Wifi },
  { page: "events", label: "Events & history", icon: Activity },
  { page: "settings", label: "Site settings", icon: Settings2 },
];
const emptyForm: FormState = { ssid: "", vlan: "", psk: "", enabled: true };
const initialAPData: APData = {
  status: null,
  telemetry: null,
  clients: [],
  sessions: [],
  events: [],
};
const apiError = (error: unknown) =>
  error instanceof Error ? error.message : "Could not reach the controller.";

function StatusBadge({ ap }: { ap: APEntry }) {
  const status = apStatus(ap);
  return (
    <Badge
      variant="outline"
      className={
        status === "Offline"
          ? "status-offline"
          : status === "Pending"
            ? "status-pending"
            : "status-online"
      }
    >
      <span className="status-dot" />
      {status}
    </Badge>
  );
}
function ClientBadge({ live }: { live: boolean }) {
  return (
    <Badge
      variant="outline"
      className={live ? "status-online" : "status-offline"}
    >
      <span className="status-dot" />
      {live ? "Associated" : "History only"}
    </Badge>
  );
}
function Empty({ title, detail }: { title: string; detail: string }) {
  return (
    <div className="empty-block">
      <CircleHelp size={19} aria-hidden="true" />
      <strong>{title}</strong>
      <span>{detail}</span>
    </div>
  );
}
function Metric({
  label,
  value,
  detail,
  icon: Icon,
}: {
  label: string;
  value: string | number;
  detail: string;
  icon: typeof LayoutDashboard;
}) {
  return (
    <Card>
      <CardContent className="metric">
        <div className="metric-top">
          <span>{label}</span>
          <Icon size={17} aria-hidden="true" />
        </div>
        <strong>{value}</strong>
        <span className="subtle">{detail}</span>
      </CardContent>
    </Card>
  );
}

function Login({ onSuccess }: { onSuccess: () => void }) {
  const [token, setToken] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function submit(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      const response = await fetch("/auth/login", {
        method: "POST",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ token }),
      });
      if (!response.ok)
        throw new Error(
          response.status === 401
            ? "The access token was not accepted."
            : (await response.text()).trim() || "Could not sign in.",
        );
      setToken("");
      onSuccess();
    } catch (cause) {
      setError(apiError(cause));
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="login-page">
      <div className="login-brand">
        <span className="brand-symbol">
          <Radio size={18} />
        </span>
        <span>Dismist</span>
      </div>
      <Card className="login-card">
        <CardHeader>
          <div className="login-icon">
            <ShieldCheck size={23} />
          </div>
          <CardTitle>Mist, dismissed.</CardTitle>
          <p className="login-description">
            A fully local controller for Juniper Mist access points.
          </p>
          <p className="subtle">
            Enter the admin access token configured on this server.
          </p>
        </CardHeader>
        <CardContent>
          <form onSubmit={submit} className="login-form">
            <Label htmlFor="admin-token">Admin access token</Label>
            <Input
              id="admin-token"
              type="password"
              autoComplete="current-password"
              autoFocus
              required
              value={token}
              onChange={(event) => setToken(event.target.value)}
              placeholder="Enter access token"
            />
            {error && (
              <p className="inline-error" role="alert">
                {error}
              </p>
            )}
            <Button type="submit" disabled={busy}>
              {busy ? "Signing in…" : "Sign in"}
            </Button>
          </form>
        </CardContent>
      </Card>
      <p className="login-foot">Self-hosted wireless management</p>
    </div>
  );
}

export default function App() {
  const [auth, setAuth] = useState<"checking" | "in" | "out" | "error">(
    "checking",
  );
  const [page, setPage] = useState<Page>("overview");
  const [sites, setSites] = useState<Site[]>([]);
  const [selectedSiteID, setSelectedSiteID] = useState("");
  const [selectedAPID, setSelectedAPID] = useState("");
  const [selectedClientMAC, setSelectedClientMAC] = useState("");
  const [aps, setAPs] = useState<APEntry[]>([]);
  const [networks, setNetworks] = useState<Network[]>([]);
  const [apData, setAPData] = useState<Record<string, APData>>({});
  const [form, setForm] = useState<FormState>(emptyForm);
  const [editing, setEditing] = useState<string | null>(null);
  const [siteForm, setSiteForm] = useState({
    name: "",
    country: "",
    timezone: "",
  });
  const [sshKeysText, setSSHKeysText] = useState("");
  const [sshKeysLoading, setSSHKeysLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [updatedAt, setUpdatedAt] = useState("");
  const [streamState, setStreamState] = useState<
    "off" | "reconnecting" | "live"
  >("off");
  const [streamTick, setStreamTick] = useState(0);
  const refreshID = useRef(0);

  useEffect(() => {
    let cancelled = false;
    void fetch("/auth/session", {
      cache: "no-store",
      credentials: "same-origin",
    })
      .then((response) => {
        if (!cancelled)
          setAuth(
            response.ok ? "in" : response.status === 401 ? "out" : "error",
          );
      })
      .catch(() => {
        if (!cancelled) setAuth("error");
      });
    const unauthorized = () => setAuth("out");
    window.addEventListener("controller:unauthorized", unauthorized);
    return () => {
      cancelled = true;
      window.removeEventListener("controller:unauthorized", unauthorized);
    };
  }, []);

  const refresh = useCallback(async () => {
    if (auth !== "in") return;
    const requestID = ++refreshID.current;
    try {
      const [siteResponse, apResponse] = await Promise.all([
        api<{ sites: Site[] }>("/api/sites"),
        api<{ aps: APEntry[] }>("/api/aps"),
      ]);
      const siteID = siteResponse.sites.some(
        (site) => site.id === selectedSiteID,
      )
        ? selectedSiteID
        : siteResponse.sites[0]?.id || "";
      const siteAPs = apResponse.aps.filter((ap) => ap.site_id === siteID);
      const [networkResponse, apResults] = await Promise.all([
        siteID
          ? api<{ networks: Network[] }>(
              `/api/sites/${encodeURIComponent(siteID)}/networks`,
            )
          : Promise.resolve({ networks: [] }),
        Promise.allSettled(
          siteAPs.map(async (ap) => {
            const base = `/api/sites/${encodeURIComponent(siteID)}/aps/${encodeURIComponent(ap.id)}`;
            const [status, telemetry, clients, sessions, events] =
              await Promise.all([
                api<Status>(`${base}/status`),
                api<Telemetry>(`${base}/telemetry`),
                api<{ clients: Client[] }>(`${base}/clients`),
                api<{ sessions: Session[] }>(`${base}/sessions?limit=100`),
                api<{ events: EventRecord[] }>(`${base}/events?limit=100`),
              ]);
            return {
              id: ap.id,
              data: {
                status,
                telemetry,
                clients: clients.clients || [],
                sessions: sessions.sessions || [],
                events: events.events || [],
              },
            };
          }),
        ),
      ]);
      if (requestID !== refreshID.current) return;
      const nextData: Record<string, APData> = {};
      apResults.forEach((result) => {
        if (result.status === "fulfilled")
          nextData[result.value.id] = result.value.data;
      });
      setSites(siteResponse.sites || []);
      setSelectedSiteID(siteID);
      setAPs(apResponse.aps || []);
      setNetworks(networkResponse.networks || []);
      setAPData(nextData);
      setSelectedAPID((current) =>
        siteAPs.some((ap) => ap.id === current)
          ? current
          : siteAPs[0]?.id || "",
      );
      if (apResults.some((result) => result.status === "rejected"))
        setError("Some AP details could not be loaded. Try refreshing.");
      else setError("");
      setUpdatedAt(new Date().toISOString());
    } catch (cause) {
      if (
        requestID === refreshID.current &&
        !(cause instanceof UnauthorizedError)
      )
        setError(apiError(cause));
    } finally {
      if (requestID === refreshID.current) setLoading(false);
    }
  }, [auth, selectedSiteID]);

  useEffect(() => {
    if (auth !== "in") return;
    void refresh();
    const timer = window.setInterval(() => void refresh(), 15000);
    return () => window.clearInterval(timer);
  }, [auth, refresh]);

  useEffect(() => {
    if (auth !== "in" || !selectedSiteID) {
      setStreamState("off");
      return;
    }
    setStreamState("reconnecting");
    if (typeof EventSource === "undefined") return;
    const source = new EventSource(
      `/api/sites/${encodeURIComponent(selectedSiteID)}/stream`,
    );
    let active = true;
    let scheduled: number | undefined;
    let lastRefresh = 0;
    const onUpdate = () => {
      if (!active || scheduled !== undefined) return;
      const delay = Math.max(100, 1000 - (Date.now() - lastRefresh));
      scheduled = window.setTimeout(() => {
        scheduled = undefined;
        if (!active) return;
        lastRefresh = Date.now();
        setStreamTick((tick) => tick + 1);
        void refresh();
      }, delay);
    };
    source.addEventListener("update", onUpdate);
    source.onopen = () => {
      if (active) setStreamState("live");
    };
    source.onerror = () => {
      if (active) setStreamState("reconnecting");
    };
    return () => {
      active = false;
      source.removeEventListener("update", onUpdate);
      source.close();
      if (scheduled !== undefined) window.clearTimeout(scheduled);
    };
  }, [auth, selectedSiteID, refresh]);

  const site = sites.find((item) => item.id === selectedSiteID);
  const siteAPs = useMemo(
    () => aps.filter((ap) => ap.site_id === selectedSiteID),
    [aps, selectedSiteID],
  );
  const selectedAP = siteAPs.find((ap) => ap.id === selectedAPID) || siteAPs[0];
  const detail = selectedAP
    ? apData[selectedAP.id] || initialAPData
    : initialAPData;
  const clients = useMemo(() => {
    const rows = new Map<string, ClientRow>();
    siteAPs.forEach((ap) => {
      const data = apData[ap.id];
      data?.sessions.forEach((session) => {
        const previous = rows.get(session.mac);
        if (
          !previous ||
          (session.connected_at || "") > (previous.connected_at || "")
        )
          rows.set(session.mac, {
            mac: session.mac,
            name: session.name,
            ip: session.ip,
            ssid: session.ssid,
            vlan_id: session.vlan_id,
            rssi: session.rssi,
            ap_id: ap.id,
            online: false,
            tx_bytes: session.tx_bytes,
            rx_bytes: session.rx_bytes,
            connected_at: session.connected_at,
            disconnected_at: session.disconnected_at,
          });
      });
      data?.clients.forEach((client) =>
        rows.set(client.mac, { ...client, ap_id: ap.id, online: true }),
      );
    });
    return [...rows.values()].sort(
      (a, b) =>
        Number(b.online) - Number(a.online) ||
        (a.name || a.mac).localeCompare(b.name || b.mac),
    );
  }, [siteAPs, apData]);
  const liveClients = clients.filter((client) => client.online);
  const selectedClient = clients.find(
    (client) => client.mac === selectedClientMAC,
  );
  const clientSessions = selectedClient
    ? siteAPs
        .flatMap((ap) => apData[ap.id]?.sessions || [])
        .filter((session) => session.mac === selectedClient.mac)
        .sort((a, b) =>
          (b.connected_at || "").localeCompare(a.connected_at || ""),
        )
    : [];
  const events = siteAPs
    .flatMap((ap) =>
      (apData[ap.id]?.events || []).map((event) => ({
        ...event,
        ap_id: ap.id,
      })),
    )
    .sort((a, b) => (b.when || "").localeCompare(a.when || ""));
  const onlineAPs = siteAPs.filter((ap) => ap.connected).length;
  const pageTitle = nav.find((item) => item.page === page)?.label || "Overview";

  useEffect(() => {
    if (site)
      setSiteForm({
        name: site.name || "",
        country: site.country || "",
        timezone: site.timezone || "",
      });
  }, [site?.id, site?.name, site?.country, site?.timezone]);

  useEffect(() => {
    if (auth !== "in" || !selectedSiteID) return;
    let cancelled = false;
    setSSHKeysLoading(true);
    setSSHKeysText("");
    void api<{ keys: string[] }>(`/api/sites/${encodeURIComponent(selectedSiteID)}/ssh-keys`)
      .then((result) => {
        if (!cancelled) setSSHKeysText((result.keys || []).join("\n"));
      })
      .catch((cause) => {
        if (!cancelled) setError(apiError(cause));
      })
      .finally(() => {
        if (!cancelled) setSSHKeysLoading(false);
      });
    return () => { cancelled = true; };
  }, [auth, selectedSiteID]);

  function resetNetwork() {
    setEditing(null);
    setForm(emptyForm);
  }
  function editNetwork(network: Network) {
    setEditing(network.id);
    setForm({
      ssid: network.ssid,
      vlan: String(network.vlan_id),
      psk: "",
      enabled: network.enabled,
    });
    document
      .getElementById("network-form")
      ?.scrollIntoView({ behavior: "smooth" });
  }
  async function mutateNetwork(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const body: Record<string, string | number | boolean> = {
        ssid: form.ssid.trim(),
        vlan_id: Number(form.vlan),
        enabled: form.enabled,
      };
      if (!editing || form.psk) body.psk = form.psk;
      await api(
        `/api/sites/${encodeURIComponent(selectedSiteID)}/networks${editing ? `/${encodeURIComponent(editing)}` : ""}`,
        { method: editing ? "PUT" : "POST", body: JSON.stringify(body) },
      );
      setNotice(editing ? "WLAN updated." : "WLAN created.");
      resetNetwork();
      await refresh();
    } catch (cause) {
      setError(apiError(cause));
    } finally {
      setBusy(false);
    }
  }
  async function changeNetwork(network: Network, remove = false) {
    if (remove && !window.confirm(`Remove ${network.ssid} from this site?`))
      return;
    setBusy(true);
    setError("");
    setNotice("");
    try {
      await api(
        `/api/sites/${encodeURIComponent(selectedSiteID)}/networks/${encodeURIComponent(network.id)}`,
        {
          method: remove ? "DELETE" : "PUT",
          ...(!remove
            ? { body: JSON.stringify({ enabled: !network.enabled }) }
            : {}),
        },
      );
      setNotice(
        remove
          ? "WLAN removed."
          : `WLAN ${network.enabled ? "disabled" : "enabled"}.`,
      );
      if (remove && editing === network.id) resetNetwork();
      await refresh();
    } catch (cause) {
      setError(apiError(cause));
    } finally {
      setBusy(false);
    }
  }
  async function saveSite(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError("");
    setNotice("");
    try {
      await api(`/api/sites/${encodeURIComponent(selectedSiteID)}`, {
        method: "PUT",
        body: JSON.stringify(siteForm),
      });
      setNotice("Site settings saved.");
      await refresh();
    } catch (cause) {
      setError(apiError(cause));
    } finally {
      setBusy(false);
    }
  }
  async function saveSSHKeys(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const keys = sshKeysText.split(/\r?\n/).map((key) => key.trim()).filter(Boolean);
      const result = await api<{ keys: string[] }>(`/api/sites/${encodeURIComponent(selectedSiteID)}/ssh-keys`, {
        method: "PUT",
        body: JSON.stringify({ keys }),
      });
      setSSHKeysText(result.keys.join("\n"));
      setNotice("Site SSH public keys saved and sent to this site's APs.");
      await refresh();
    } catch (cause) {
      setError(apiError(cause));
    } finally {
      setBusy(false);
    }
  }
  async function logout() {
    try {
      await fetch("/auth/logout", {
        method: "POST",
        credentials: "same-origin",
      });
    } finally {
      setAuth("out");
      setSites([]);
      setAPs([]);
      setAPData({});
    }
  }

  if (auth === "checking")
    return (
      <div className="center-state">
        <RefreshCw className="spin" size={20} /> Checking session…
      </div>
    );
  if (auth === "error")
    return (
      <div className="center-state">
        <Card>
          <CardHeader>
            <CardTitle>Controller unavailable</CardTitle>
          </CardHeader>
          <CardContent>
            <p>Could not check the admin session.</p>
            <Button onClick={() => window.location.reload()}>Retry</Button>
          </CardContent>
        </Card>
      </div>
    );
  if (auth === "out")
    return (
      <Login
        onSuccess={() => {
          setLoading(true);
          setAuth("in");
        }}
      />
    );

  return (
    <div className="admin-shell">
      <aside className="sidebar" aria-label="Main navigation">
        <div className="sidebar-brand">
          <span className="brand-symbol">
            <Radio size={18} />
          </span>
          <span>
            <strong>Dismist</strong>
            <small>Wireless management</small>
          </span>
        </div>
        <div className="nav-caption">WORKSPACE</div>
        <nav className="main-nav">
          {nav.map((item) => (
            <button
              key={item.page}
              type="button"
              className={`nav-item ${page === item.page ? "active" : ""}`}
              onClick={() => setPage(item.page)}
            >
              <item.icon size={18} aria-hidden="true" />
              {item.label}
            </button>
          ))}
        </nav>
        <div className="sidebar-foot">
          <span className="sidebar-foot-label">SELF-HOSTED CONTROLLER</span>
          <button
            type="button"
            className="nav-item"
            onClick={() => void logout()}
          >
            <LogOut size={17} /> Sign out
          </button>
        </div>
      </aside>
      <div className="workspace">
        <header className="workspace-header">
          <div className="mobile-brand">
            <span className="brand-symbol">
              <Radio size={17} />
            </span>
            <strong>Dismist</strong>
          </div>
          <div className="breadcrumbs">
            Workspace <span>/</span> {pageTitle}
          </div>
          <div className="header-actions">
            {selectedSiteID && (
              <span
                className={`stream-indicator ${streamState}`}
                title="UI data stream"
              >
                <span className="stream-dot" />
                <span className="stream-label">UI stream</span>
                <span>{streamState === "live" ? "Live" : "Reconnecting"}</span>
              </span>
            )}
            <span className="updated-label">
              {updatedAt
                ? `Updated ${timeLabel(updatedAt)}`
                : "Waiting for data"}
            </span>
            <Button
              variant="outline"
              size="icon"
              aria-label="Refresh data"
              title="Refresh data"
              onClick={() => void refresh()}
            >
              <RefreshCw size={16} />
            </Button>
            <Button
              className="mobile-logout"
              variant="ghost"
              size="icon"
              aria-label="Sign out"
              title="Sign out"
              onClick={() => void logout()}
            >
              <LogOut size={16} />
            </Button>
          </div>
        </header>
        <main className="workspace-main">
          <div className="page-heading">
            <div>
              <p className="eyebrow">SITE WORKSPACE</p>
              <h1>{page === "overview" ? "Mist, dismissed." : pageTitle}</h1>
              <p>
                {page === "overview"
                  ? "A fully local controller for Juniper Mist access points."
                  : page === "aps"
                    ? "Devices assigned to this site and their reported status."
                    : page === "clients"
                      ? "Associated devices and recorded client sessions."
                      : page === "wlans"
                        ? "Wireless networks shared by the site’s access points."
                        : page === "events"
                          ? "Stored connection events and AP history."
                          : "Site identity and advanced defaults."}
              </p>
            </div>
            <div className="page-heading-side">
              <SiteBar
                sites={sites}
                selectedID={selectedSiteID}
                onSelect={(id) => {
                  setLoading(true);
                  setNetworks([]);
                  setAPData({});
                  setSelectedSiteID(id);
                  setSelectedAPID("");
                  setSelectedClientMAC("");
                  resetNetwork();
                }}
                onCreated={(id) => {
                  setLoading(true);
                  setNetworks([]);
                  setAPData({});
                  setSelectedSiteID(id);
                  setSelectedAPID("");
                  resetNetwork();
                }}
              />
            </div>
          </div>
          {error && (
            <div className="app-alert error" role="alert">
              {error}
            </div>
          )}
          {notice && (
            <div className="app-alert success" role="status">
              {notice}
            </div>
          )}
          {loading ? (
            <div className="loading-block">
              <RefreshCw size={18} className="spin" /> Loading site data…
            </div>
          ) : !site ? (
            <Empty
              title="No sites yet"
              detail="Create a site with the site selector to begin configuring your controller."
            />
          ) : (
            <>
              {page === "overview" && (
                <>
                  <div className="metrics-grid">
                    <Metric
                      label="Access points"
                      value={siteAPs.length}
                      detail={`${onlineAPs} connected · ${siteAPs.length - onlineAPs} offline`}
                      icon={Router}
                    />
                    <Metric
                      label="Associated clients"
                      value={liveClients.length}
                      detail={
                        error
                          ? "Partial data · some AP reports unavailable"
                          : "Currently reported across site APs"
                      }
                      icon={Users}
                    />
                    <Metric
                      label="WLANs"
                      value={networks.length}
                      detail={`${networks.filter((network) => network.enabled).length} enabled`}
                      icon={Wifi}
                    />
                    <Metric
                      label="Events"
                      value={events.length}
                      detail="Recent stored AP events"
                      icon={Activity}
                    />
                  </div>
                  <div className="dashboard-grid">
                    <Card>
                      <CardHeader className="section-head">
                        <div>
                          <CardTitle>Access points</CardTitle>
                          <p className="subtle">
                            Connection and configuration status
                          </p>
                        </div>
                        <Button
                          variant="ghost"
                          size="sm"
                          onClick={() => setPage("aps")}
                        >
                          View all
                        </Button>
                      </CardHeader>
                      <CardContent className="flush">
                        {siteAPs.length ? (
                          <div className="table-scroll">
                            <Table>
                              <TableHeader>
                                <TableRow>
                                  <TableHead>Access point</TableHead>
                                  <TableHead>Status</TableHead>
                                  <TableHead>Clients</TableHead>
                                  <TableHead>Last seen</TableHead>
                                </TableRow>
                              </TableHeader>
                              <TableBody>
                                {siteAPs.slice(0, 6).map((ap) => (
                                  <TableRow
                                    key={ap.id}
                                    className="clickable-row"
                                    onClick={() => {
                                      setSelectedAPID(ap.id);
                                      setPage("aps");
                                    }}
                                  >
                                    <TableCell>
                                      <strong>{ap.name || ap.id}</strong>
                                      <span className="cell-secondary">
                                        {ap.model || "Model unavailable"}
                                      </span>
                                    </TableCell>
                                    <TableCell>
                                      <StatusBadge ap={ap} />
                                    </TableCell>
                                    <TableCell>
                                      {apData[ap.id]?.clients.length ?? 0}
                                    </TableCell>
                                    <TableCell>
                                      {timeLabel(ap.last_seen)}
                                    </TableCell>
                                  </TableRow>
                                ))}
                              </TableBody>
                            </Table>
                          </div>
                        ) : (
                          <Empty
                            title="No access points assigned"
                            detail="APs registered to this site will appear here."
                          />
                        )}
                      </CardContent>
                    </Card>
                    <Card>
                      <CardHeader className="section-head">
                        <div>
                          <CardTitle>Recent events</CardTitle>
                          <p className="subtle">Across this site’s APs</p>
                        </div>
                        <Button
                          variant="ghost"
                          size="sm"
                          onClick={() => setPage("events")}
                        >
                          View all
                        </Button>
                      </CardHeader>
                      <CardContent className="flush">
                        {events.length ? (
                          <div className="event-list">
                            {events.slice(0, 6).map((event, index) => (
                              <div
                                className="event-list-row"
                                key={`${event.when}-${index}`}
                              >
                                <span className="event-icon">
                                  <Activity size={15} />
                                </span>
                                <div>
                                  <strong>{event.title || event.kind}</strong>
                                  <span>
                                    {event.detail ||
                                      event.client_mac ||
                                      siteAPs.find(
                                        (ap) => ap.id === event.ap_id,
                                      )?.name ||
                                      "AP event"}
                                  </span>
                                </div>
                                <time>{timeLabel(event.when)}</time>
                              </div>
                            ))}
                          </div>
                        ) : (
                          <Empty
                            title="No events recorded"
                            detail="Events appear when an AP reports connection activity."
                          />
                        )}
                      </CardContent>
                    </Card>
                  </div>
                  {selectedAP && (
                    <>
                      <p className="overview-history-label">
                        Client history · {selectedAP.name || selectedAP.id}
                      </p>
                      <HistoryPanel
                        siteID={selectedSiteID}
                        apID={selectedAP.id}
                        refreshSignal={streamTick}
                      />
                    </>
                  )}
                </>
              )}
              {page === "aps" && (
                <>
                  <APInventory
                    aps={aps}
                    sites={sites}
                    selectedSiteID={selectedSiteID}
                    selectedAPID={selectedAP?.id || ""}
                    clientCounts={Object.fromEntries(
                      siteAPs
                        .filter((ap) => apData[ap.id])
                        .map((ap) => [ap.id, apData[ap.id].clients.length]),
                    )}
                    onSelectAP={setSelectedAPID}
                    onMoved={(siteID, apID) => {
                      setSelectedSiteID(siteID);
                      setSelectedAPID(apID);
                    }}
                    onCreated={(apID) => {
                      setSelectedAPID(apID);
                      void refresh();
                    }}
                  />
                  {selectedAP && (
                    <>
                      <Card className="detail-card">
                        <CardHeader className="detail-header">
                          <div>
                            <p className="eyebrow">ACCESS POINT DETAIL</p>
                            <CardTitle>
                              {selectedAP.name || selectedAP.id}
                            </CardTitle>
                            <p className="subtle mono">{selectedAP.id}</p>
                          </div>
                          <div className="detail-actions">
                            <StatusBadge ap={selectedAP} />
                            <APEditor
                              key={selectedAP.id}
                              ap={selectedAP}
                              onSaved={async (id) => {
                                setSelectedAPID(id);
                                await refresh();
                                setNotice("Access point updated.");
                              }}
                            />
                          </div>
                        </CardHeader>
                        <CardContent>
                          <div className="detail-grid">
                            <div>
                              <span>Model</span>
                              <strong>
                                {selectedAP.model || "Unavailable"}
                              </strong>
                            </div>
                            <div>
                              <span>Firmware</span>
                              <strong>
                                {detail.status?.ap.firmware ||
                                  selectedAP.firmware ||
                                  "Unavailable"}
                              </strong>
                            </div>
                            <div>
                              <span>LAN IP</span>
                              <strong>
                                {selectedAP.lan_ip || "Not reported yet"}
                              </strong>
                            </div>
                            <div>
                              <span>Public address</span>
                              <strong>
                                {selectedAP.remote_address || "Not seen yet"}
                              </strong>
                            </div>
                            <div>
                              <span>Source pin</span>
                              <strong>
                                {selectedAP.management_ip || "Any address"}
                              </strong>
                            </div>
                            <div>
                              <span>Last seen</span>
                              <strong>
                                {timeLabel(
                                  detail.status?.ap.last_seen ||
                                    selectedAP.last_seen,
                                )}
                              </strong>
                            </div>
                            <div>
                              <span>Config revision</span>
                              <strong>
                                {selectedAP.config_revision ??
                                  detail.status?.desired_revision ??
                                  "Unavailable"}
                              </strong>
                            </div>
                            <div>
                              <span>Applied revision</span>
                              <strong>
                                {detail.status?.ap.applied_revision ??
                                  selectedAP.applied_revision ??
                                  "Unavailable"}
                              </strong>
                            </div>
                            <div>
                              <span>Uptime</span>
                              <strong>
                                {durationLabel(
                                  detail.telemetry?.uptime_seconds,
                                )}
                              </strong>
                            </div>
                            <div>
                              <span>Associated clients</span>
                              <strong>{detail.clients.length}</strong>
                            </div>
                          </div>
                          {detail.status?.ap.last_error && (
                            <div className="app-alert error" role="alert">
                              Configuration error: {detail.status.ap.last_error}
                            </div>
                          )}
                        </CardContent>
                      </Card>
                      <div className="two-col">
                        <Card>
                          <CardHeader>
                            <CardTitle>Radios</CardTitle>
                          </CardHeader>
                          <CardContent>
                            {detail.telemetry?.radios?.length ? (
                              detail.telemetry.radios.map((radio, index) => (
                                <div
                                  className="property-row"
                                  key={`${radio.interface}-${index}`}
                                >
                                  <span>{radio.interface}</span>
                                  <strong>
                                    {radio.width_mhz
                                      ? `${radio.width_mhz} MHz`
                                      : "Width unavailable"}
                                  </strong>
                                </div>
                              ))
                            ) : (
                              <Empty
                                title="Radio data unavailable"
                                detail="A full AP telemetry report has not arrived yet."
                              />
                            )}
                          </CardContent>
                        </Card>
                        <Card>
                          <CardHeader>
                            <CardTitle>Broadcast interfaces</CardTitle>
                          </CardHeader>
                          <CardContent>
                            {detail.telemetry?.vaps?.length ? (
                              detail.telemetry.vaps.map((vap, index) => (
                                <div
                                  className="property-row"
                                  key={`${vap.interface}-${index}`}
                                >
                                  <span>
                                    <strong>{vap.ssid}</strong>
                                    <small>{vap.interface}</small>
                                  </span>
                                  <span className="mono">
                                    {vap.bssid || "BSSID unavailable"}
                                  </span>
                                </div>
                              ))
                            ) : (
                              <Empty
                                title="No interfaces reported"
                                detail="WLAN broadcast details appear after a full AP report."
                              />
                            )}
                          </CardContent>
                        </Card>
                      </div>
                      <HistoryPanel
                        siteID={selectedSiteID}
                        apID={selectedAP.id}
                        refreshSignal={streamTick}
                      />
                      <AdvancedEditor
                        siteID={selectedSiteID}
                        apID={selectedAP.id}
                        onApplied={refresh}
                      />
                    </>
                  )}
                </>
              )}
              {page === "clients" && (
                <>
                  <Card>
                    <CardHeader className="section-head">
                      <div>
                        <CardTitle>Client inventory</CardTitle>
                        <p className="subtle">
                          {liveClients.length} associated · {clients.length}{" "}
                          observed in live or stored sessions
                        </p>
                      </div>
                      <ListFilter size={18} className="muted-icon" />
                    </CardHeader>
                    <CardContent className="flush">
                      {clients.length ? (
                        <div className="table-scroll">
                          <Table>
                            <TableHeader>
                              <TableRow>
                                <TableHead>Client</TableHead>
                                <TableHead>Status</TableHead>
                                <TableHead>AP</TableHead>
                                <TableHead>SSID / VLAN</TableHead>
                                <TableHead>IP address</TableHead>
                                <TableHead>Signal</TableHead>
                              </TableRow>
                            </TableHeader>
                            <TableBody>
                              {clients.map((client) => (
                                <TableRow
                                  key={client.mac}
                                  className={`clickable-row ${selectedClientMAC === client.mac ? "selected-row" : ""}`}
                                  onClick={() =>
                                    setSelectedClientMAC(client.mac)
                                  }
                                >
                                  <TableCell>
                                    <strong>{client.name || client.mac}</strong>
                                    {client.name && (
                                      <span className="cell-secondary mono">
                                        {client.mac}
                                      </span>
                                    )}
                                  </TableCell>
                                  <TableCell>
                                    <ClientBadge live={client.online} />
                                  </TableCell>
                                  <TableCell>
                                    {siteAPs.find(
                                      (ap) => ap.id === client.ap_id,
                                    )?.name || client.ap_id}
                                  </TableCell>
                                  <TableCell>
                                    {client.ssid || "Unavailable"}{" "}
                                    <span className="cell-secondary">
                                      VLAN {client.vlan_id}
                                    </span>
                                  </TableCell>
                                  <TableCell>
                                    {client.ip || "Unavailable"}
                                  </TableCell>
                                  <TableCell>
                                    {client.rssi != null
                                      ? `${client.rssi} dBm`
                                      : "Unavailable"}
                                  </TableCell>
                                </TableRow>
                              ))}
                            </TableBody>
                          </Table>
                        </div>
                      ) : (
                        <Empty
                          title="No clients observed"
                          detail="Associated clients and stored sessions will appear when APs report them."
                        />
                      )}
                    </CardContent>
                  </Card>
                  {selectedClient && (
                    <Card className="detail-card">
                      <CardHeader className="detail-header">
                        <div>
                          <p className="eyebrow">CLIENT DETAIL</p>
                          <CardTitle>
                            {selectedClient.name || selectedClient.mac}
                          </CardTitle>
                          <p className="subtle mono">{selectedClient.mac}</p>
                        </div>
                        <ClientBadge live={selectedClient.online} />
                      </CardHeader>
                      <CardContent>
                        <div className="detail-grid">
                          <div>
                            <span>Access point</span>
                            <strong>
                              {siteAPs.find(
                                (ap) => ap.id === selectedClient.ap_id,
                              )?.name || selectedClient.ap_id}
                            </strong>
                          </div>
                          <div>
                            <span>IP address</span>
                            <strong>
                              {selectedClient.ip || "Unavailable"}
                            </strong>
                          </div>
                          <div>
                            <span>SSID</span>
                            <strong>
                              {selectedClient.ssid || "Unavailable"}
                            </strong>
                          </div>
                          <div>
                            <span>VLAN</span>
                            <strong>{selectedClient.vlan_id}</strong>
                          </div>
                          <div>
                            <span>Signal</span>
                            <strong>
                              {selectedClient.rssi != null
                                ? `${selectedClient.rssi} dBm`
                                : "Unavailable"}
                            </strong>
                          </div>
                          <div>
                            <span>Connected time</span>
                            <strong>
                              {selectedClient.online
                                ? durationLabel(
                                    selectedClient.connected_seconds,
                                  )
                                : "Not associated"}
                            </strong>
                          </div>
                          <div>
                            <span>Received</span>
                            <strong>
                              {bytesLabel(selectedClient.rx_bytes)}
                            </strong>
                          </div>
                          <div>
                            <span>Sent</span>
                            <strong>
                              {bytesLabel(selectedClient.tx_bytes)}
                            </strong>
                          </div>
                        </div>
                        <h3 className="subsection-title">Recorded sessions</h3>
                        {clientSessions.length ? (
                          <div className="table-scroll">
                            <Table>
                              <TableHeader>
                                <TableRow>
                                  <TableHead>Connected</TableHead>
                                  <TableHead>Disconnected</TableHead>
                                  <TableHead>AP</TableHead>
                                  <TableHead>Reason</TableHead>
                                </TableRow>
                              </TableHeader>
                              <TableBody>
                                {clientSessions
                                  .slice(0, 12)
                                  .map((session, index) => (
                                    <TableRow
                                      key={`${session.ap_id}-${session.connected_at}-${index}`}
                                    >
                                      <TableCell>
                                        {timeLabel(session.connected_at)}
                                      </TableCell>
                                      <TableCell>
                                        {session.disconnected_at
                                          ? timeLabel(session.disconnected_at)
                                          : "Current or not reported"}
                                      </TableCell>
                                      <TableCell>
                                        {siteAPs.find(
                                          (ap) => ap.id === session.ap_id,
                                        )?.name || session.ap_id}
                                      </TableCell>
                                      <TableCell>
                                        {session.end_reason || "—"}
                                      </TableCell>
                                    </TableRow>
                                  ))}
                              </TableBody>
                            </Table>
                          </div>
                        ) : (
                          <Empty
                            title="No session history"
                            detail="Sessions appear when an AP reports client association."
                          />
                        )}
                        <h3 className="subsection-title">Client events</h3>
                        {events.filter(
                          (event) => event.client_mac === selectedClient.mac,
                        ).length ? (
                          <div className="event-list">
                            {events
                              .filter(
                                (event) =>
                                  event.client_mac === selectedClient.mac,
                              )
                              .slice(0, 12)
                              .map((event, index) => (
                                <div
                                  className="event-list-row"
                                  key={`${event.when}-${index}`}
                                >
                                  <span className="event-icon">
                                    <Activity size={15} />
                                  </span>
                                  <div>
                                    <strong>{event.title || event.kind}</strong>
                                    <span>
                                      {event.detail || "No additional detail"}
                                    </span>
                                  </div>
                                  <time>{timeLabel(event.when)}</time>
                                </div>
                              ))}
                          </div>
                        ) : (
                          <Empty
                            title="No client events"
                            detail="AP events for this client will appear here when reported."
                          />
                        )}
                      </CardContent>
                    </Card>
                  )}
                </>
              )}
              {page === "wlans" && (
                <>
                  <Card>
                    <CardHeader className="section-head">
                      <div>
                        <CardTitle>Wireless LANs</CardTitle>
                        <p className="subtle">
                          Changes apply to access points assigned to this site.
                        </p>
                      </div>
                      <Badge variant="secondary">{networks.length} total</Badge>
                    </CardHeader>
                    <CardContent className="flush">
                      {networks.length ? (
                        <div className="table-scroll">
                          <Table>
                            <TableHeader>
                              <TableRow>
                                <TableHead>SSID</TableHead>
                                <TableHead>VLAN</TableHead>
                                <TableHead>Security</TableHead>
                                <TableHead>Status</TableHead>
                                <TableHead className="actions-col">
                                  Actions
                                </TableHead>
                              </TableRow>
                            </TableHeader>
                            <TableBody>
                              {networks.map((network) => (
                                <TableRow key={network.id}>
                                  <TableCell>
                                    <strong>{network.ssid}</strong>
                                  </TableCell>
                                  <TableCell>{network.vlan_id}</TableCell>
                                  <TableCell>
                                    {network.has_psk
                                      ? "WPA2 personal"
                                      : "Passphrase unavailable"}
                                  </TableCell>
                                  <TableCell>
                                    <Badge
                                      variant="outline"
                                      className={
                                        network.enabled
                                          ? "status-online"
                                          : "status-offline"
                                      }
                                    >
                                      {network.enabled ? "Enabled" : "Disabled"}
                                    </Badge>
                                  </TableCell>
                                  <TableCell className="actions-col">
                                    <Button
                                      variant="ghost"
                                      size="sm"
                                      onClick={() => editNetwork(network)}
                                    >
                                      Edit
                                    </Button>
                                    <Button
                                      variant="ghost"
                                      size="sm"
                                      disabled={busy}
                                      onClick={() =>
                                        void changeNetwork(network)
                                      }
                                    >
                                      {network.enabled ? "Disable" : "Enable"}
                                    </Button>
                                    <Button
                                      variant="ghost"
                                      size="sm"
                                      className="danger-text"
                                      disabled={busy}
                                      onClick={() =>
                                        void changeNetwork(network, true)
                                      }
                                    >
                                      Remove
                                    </Button>
                                  </TableCell>
                                </TableRow>
                              ))}
                            </TableBody>
                          </Table>
                        </div>
                      ) : (
                        <Empty
                          title="No WLANs configured"
                          detail="Add a WLAN below to broadcast from this site’s APs."
                        />
                      )}
                    </CardContent>
                  </Card>
                  <Card id="network-form" className="form-card">
                    <CardHeader>
                      <CardTitle>
                        {editing ? "Edit WLAN" : "Add WLAN"}
                      </CardTitle>
                      <p className="subtle">
                        Use a site wide SSID, tagged client VLAN, and private
                        passphrase.
                      </p>
                    </CardHeader>
                    <CardContent>
                      <form onSubmit={mutateNetwork}>
                        <div className="form-grid">
                          <div className="field">
                            <Label htmlFor="ssid">SSID</Label>
                            <Input
                              id="ssid"
                              autoComplete="off"
                              maxLength={32}
                              required
                              value={form.ssid}
                              onChange={(event) =>
                                setForm({ ...form, ssid: event.target.value })
                              }
                              placeholder="Office Wi-Fi"
                            />
                          </div>
                          <div className="field">
                            <Label htmlFor="vlan">Client VLAN</Label>
                            <Input
                              id="vlan"
                              type="number"
                              min="1"
                              max="4094"
                              required
                              value={form.vlan}
                              onChange={(event) =>
                                setForm({ ...form, vlan: event.target.value })
                              }
                              placeholder="1–4094"
                            />
                          </div>
                          <div className="field span-2">
                            <Label htmlFor="psk">WPA2 passphrase</Label>
                            <Input
                              id="psk"
                              type="password"
                              {...writeOnlySecret}
                              minLength={editing ? undefined : 8}
                              maxLength={63}
                              required={!editing}
                              value={form.psk}
                              onChange={(event) =>
                                setForm({ ...form, psk: event.target.value })
                              }
                              placeholder={
                                editing
                                  ? "Leave blank to keep current passphrase"
                                  : "8–63 characters"
                              }
                            />
                          </div>
                        </div>
                        <div className="form-bottom">
                          <label className="checkbox-line">
                            <input
                              type="checkbox"
                              checked={form.enabled}
                              onChange={(event) =>
                                setForm({
                                  ...form,
                                  enabled: event.target.checked,
                                })
                              }
                            />
                            Enable broadcast
                          </label>
                          <div className="button-group">
                            {editing && (
                              <Button
                                type="button"
                                variant="outline"
                                onClick={resetNetwork}
                              >
                                Cancel
                              </Button>
                            )}
                            <Button type="submit" disabled={busy}>
                              {busy
                                ? "Saving…"
                                : editing
                                  ? "Save changes"
                                  : "Add WLAN"}
                            </Button>
                          </div>
                        </div>
                      </form>
                    </CardContent>
                  </Card>
                  <Card className="form-card">
                    <CardHeader>
                      <CardTitle>AP SSH public keys</CardTitle>
                      <p className="subtle">One OpenSSH public key per line. Keys are added to this site's AP configuration after each AP connects. Leave empty to remove site keys while retaining template keys.</p>
                    </CardHeader>
                    <CardContent>
                      <form onSubmit={saveSSHKeys}>
                        <div className="field">
                          <Label htmlFor="site-ssh-keys">Public keys</Label>
                          <textarea
                            id="site-ssh-keys"
                            className="min-h-36 w-full rounded-md border border-input bg-transparent p-3 font-mono text-xs"
                            spellCheck={false}
                            value={sshKeysText}
                            disabled={sshKeysLoading}
                            onChange={(event) => setSSHKeysText(event.target.value)}
                            placeholder="ssh-ed25519 AAAA..."
                          />
                        </div>
                        <div className="form-bottom">
                          <span className="subtle">{sshKeysLoading ? "Loading keys…" : "Public keys only; private keys are rejected."}</span>
                          <Button type="submit" disabled={busy || sshKeysLoading}>
                            {busy ? "Saving…" : "Save SSH keys"}
                          </Button>
                        </div>
                      </form>
                    </CardContent>
                  </Card>
                  <AdvancedEditor
                    siteID={selectedSiteID}
                    apID={selectedAP?.id}
                    networkID={editing}
                    onApplied={refresh}
                  />
                </>
              )}
              {page === "events" && (
                <>
                  <Card>
                    <CardHeader className="section-head">
                      <div>
                        <CardTitle>Event history</CardTitle>
                        <p className="subtle">
                          Recent events stored for APs in this site.
                        </p>
                      </div>
                      <Badge variant="secondary">{events.length} events</Badge>
                    </CardHeader>
                    <CardContent className="flush">
                      {events.length ? (
                        <div className="table-scroll">
                          <Table>
                            <TableHeader>
                              <TableRow>
                                <TableHead>Time</TableHead>
                                <TableHead>Event</TableHead>
                                <TableHead>AP</TableHead>
                                <TableHead>Client</TableHead>
                              </TableRow>
                            </TableHeader>
                            <TableBody>
                              {events.map((event, index) => (
                                <TableRow
                                  key={`${event.ap_id}-${event.when}-${index}`}
                                >
                                  <TableCell>{timeLabel(event.when)}</TableCell>
                                  <TableCell>
                                    <strong>{event.title || event.kind}</strong>
                                    {event.detail && (
                                      <span className="cell-secondary">
                                        {event.detail}
                                      </span>
                                    )}
                                  </TableCell>
                                  <TableCell>
                                    {siteAPs.find((ap) => ap.id === event.ap_id)
                                      ?.name || event.ap_id}
                                  </TableCell>
                                  <TableCell className="mono">
                                    {event.client_mac || "—"}
                                  </TableCell>
                                </TableRow>
                              ))}
                            </TableBody>
                          </Table>
                        </div>
                      ) : (
                        <Empty
                          title="No events recorded"
                          detail="Events will appear as APs report connection activity."
                        />
                      )}
                    </CardContent>
                  </Card>
                  {siteAPs.length > 0 && (
                    <>
                      <div className="history-select">
                        <Label htmlFor="history-ap">AP history</Label>
                        <select
                          id="history-ap"
                          value={selectedAP?.id || ""}
                          onChange={(event) =>
                            setSelectedAPID(event.target.value)
                          }
                        >
                          {siteAPs.map((ap) => (
                            <option key={ap.id} value={ap.id}>
                              {ap.name || ap.id}
                            </option>
                          ))}
                        </select>
                      </div>
                      <HistoryPanel
                        siteID={selectedSiteID}
                        apID={selectedAP?.id}
                        refreshSignal={streamTick}
                      />
                    </>
                  )}
                </>
              )}
              {page === "settings" && (
                <>
                  <Card className="form-card">
                    <CardHeader>
                      <CardTitle>Site details</CardTitle>
                      <p className="subtle">
                        These settings are stored with the site and apply to its
                        APs.
                      </p>
                    </CardHeader>
                    <CardContent>
                      <form onSubmit={saveSite}>
                        <div className="form-grid">
                          <div className="field span-2">
                            <Label htmlFor="site-name">Site name</Label>
                            <Input
                              id="site-name"
                              required
                              maxLength={80}
                              value={siteForm.name}
                              onChange={(event) =>
                                setSiteForm({
                                  ...siteForm,
                                  name: event.target.value,
                                })
                              }
                            />
                          </div>
                          <div className="field">
                            <Label htmlFor="site-country">Country code</Label>
                            <Input
                              id="site-country"
                              maxLength={2}
                              value={siteForm.country}
                              onChange={(event) =>
                                setSiteForm({
                                  ...siteForm,
                                  country: event.target.value.toUpperCase(),
                                })
                              }
                              placeholder="AU"
                            />
                          </div>
                          <div className="field">
                            <Label htmlFor="site-timezone">Time zone</Label>
                            <Input
                              id="site-timezone"
                              value={siteForm.timezone}
                              onChange={(event) =>
                                setSiteForm({
                                  ...siteForm,
                                  timezone: event.target.value,
                                })
                              }
                              placeholder="Australia/Sydney"
                            />
                          </div>
                        </div>
                        <div className="form-bottom">
                          <span className="subtle">
                            Site ID: <code>{site.id}</code>
                          </span>
                          <Button type="submit" disabled={busy}>
                            {busy ? "Saving…" : "Save site settings"}
                          </Button>
                        </div>
                      </form>
                    </CardContent>
                  </Card>
                  <AdvancedEditor
                    siteID={selectedSiteID}
                    apID={selectedAP?.id}
                    onApplied={refresh}
                  />
                </>
              )}
            </>
          )}
        </main>
      </div>
    </div>
  );
}
