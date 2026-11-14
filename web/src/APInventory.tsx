import { api } from "./api";
import { useState, type FormEvent } from "react";
import type { Site } from "./SiteBar";
import { Plus } from "lucide-react";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import { Label } from "./components/ui/label";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "./components/ui/dialog";
import { timeLabel } from "./api";
import SecretField from "./SecretField";
import { secretProblem } from "./secret";

export type APEntry = {
  id: string;
  name: string;
  site_id: string;
  management_ip?: string;
  lan_ip?: string;
  remote_address?: string;
  has_secret?: boolean;
  model: string;
  firmware: string;
  connected: boolean;
  last_seen: string;
  applied_revision: number;
  config_revision?: number;
};

export function apStatus(
  ap: APEntry,
): "Dismisted" | "Pending" | "Connected" | "Offline" {
  if (!ap.connected) return "Offline";
  if (!ap.config_revision) return "Connected";
  return ap.applied_revision >= ap.config_revision ? "Dismisted" : "Pending";
}

type Props = {
  aps: APEntry[];
  sites: Site[];
  selectedSiteID: string;
  selectedAPID: string;
  onSelectAP: (id: string) => void;
  onMoved: (siteID: string, apID: string) => void;
  onCreated: (apID: string) => void;
  clientCounts: Record<string, number>;
};

export default function APInventory({
  aps,
  sites,
  selectedSiteID,
  selectedAPID,
  onSelectAP,
  onMoved,
  onCreated,
  clientCounts,
}: Props) {
  const [targets, setTargets] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [createOpen, setCreateOpen] = useState(false);
  const [createError, setCreateError] = useState("");
  const blankAP = { id: "", name: "", management_ip: "", secret: "" };
  const [newAP, setNewAP] = useState(blankAP);
  const shown = aps.filter((ap) => ap.site_id === selectedSiteID);

  async function create(event: FormEvent) {
    event.preventDefault();
    const problem = secretProblem(newAP.secret);
    if (problem) {
      setCreateError(problem);
      return;
    }
    setBusy("create");
    setCreateError("");
    try {
      const id = newAP.id.trim().toLowerCase();
      const created = await api<{ ap: APEntry }>("/api/aps", {
        method: "POST",
        body: JSON.stringify({
          id,
          name: newAP.name.trim(),
          site_id: selectedSiteID,
          management_ip: newAP.management_ip.trim(),
          secret: newAP.secret.trim(),
        }),
      });
      setCreateOpen(false);
      setNewAP(blankAP);
      onCreated(created.ap.id);
    } catch (cause) {
      setCreateError(
        cause instanceof Error ? cause.message : "Could not register AP",
      );
    } finally {
      setBusy("");
    }
  }

  async function move(ap: APEntry) {
    const target = targets[ap.id];
    if (!target || target === ap.site_id) return;
    const name = sites.find((site) => site.id === target)?.name || target;
    if (!window.confirm(`Move ${ap.name || ap.id} to ${name}?`)) return;
    setBusy(ap.id);
    setError("");
    try {
      await api(`/api/aps/${encodeURIComponent(ap.id)}/site`, {
        method: "PUT",
        body: JSON.stringify({ site_id: target }),
      });
      onMoved(target, ap.id);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "AP move failed");
    } finally {
      setBusy("");
    }
  }

  return (
    <section className="panel ap-inventory">
      <div className="panel-heading">
        <div>
          <span className="section-kicker">Site inventory</span>
          <h3>Access points</h3>
        </div>
        <div className="panel-heading-actions">
          <span className="count-badge">{shown.length} in site</span>
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            <Plus size={14} /> Register AP
          </Button>
        </div>
      </div>
      {error && (
        <div className="alert error" role="alert">
          {error}
        </div>
      )}
      {shown.length ? (
        shown.map((ap) => (
          <div className="ap-inventory-row" key={ap.id}>
            <button
              className={`ap-choice ${selectedAPID === ap.id ? "active" : ""}`}
              type="button"
              onClick={() => onSelectAP(ap.id)}
            >
              <span className={`mini-dot ${ap.connected ? "connected" : ""}`} />
              <span>
                <strong>{ap.name || ap.id}</strong>
                <small>
                  {ap.model || "AP"} · {addressLabel(ap)} · {ap.id}
                </small>
              </span>
            </button>
            <div className="ap-row-facts">
              <span>
                <small>Status</small>
                <strong>{apStatus(ap)}</strong>
              </span>
              <span>
                <small>Firmware</small>
                <strong>{ap.firmware || "Unavailable"}</strong>
              </span>
              <span>
                <small>Clients</small>
                <strong>{clientCounts[ap.id] ?? "Unavailable"}</strong>
              </span>
              <span>
                <small>Last seen</small>
                <strong>{timeLabel(ap.last_seen)}</strong>
              </span>
              <span>
                <small>Config</small>
                <strong>
                  {ap.applied_revision || 0} / {ap.config_revision || 0}
                </strong>
              </span>
            </div>
            {sites.length > 1 && (
              <div className="ap-move">
                <select
                  aria-label={`Move ${ap.name || ap.id} to site`}
                  value={targets[ap.id] || ""}
                  onChange={(event) =>
                    setTargets({ ...targets, [ap.id]: event.target.value })
                  }
                >
                  <option value="">Move to…</option>
                  {sites
                    .filter((site) => site.id !== ap.site_id)
                    .map((site) => (
                      <option key={site.id} value={site.id}>
                        {site.name}
                      </option>
                    ))}
                </select>
                <button
                  type="button"
                  className="text-button"
                  disabled={busy === ap.id || !targets[ap.id]}
                  onClick={() => void move(ap)}
                >
                  {busy === ap.id ? "Moving…" : "Move"}
                </button>
              </div>
            )}
          </div>
        ))
      ) : (
        <p className="empty-activity">No APs belong to this site yet.</p>
      )}
      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>Register access point</DialogTitle>
            <DialogDescription>
              Add an AP to this site with its 32-byte EEPROM secret. The model
              and firmware are read from the AP when it connects. The secret is
              stored on this controller and is never shown again.
            </DialogDescription>
          </DialogHeader>
          <form onSubmit={create} className="register-form">
            <div className="field">
              <Label htmlFor="ap-id">AP ID / MAC address</Label>
              <Input
                id="ap-id"
                required
                value={newAP.id}
                onChange={(event) =>
                  setNewAP({ ...newAP, id: event.target.value })
                }
                placeholder="02-aa-bb-cc-dd-ee"
                autoComplete="off"
                autoCapitalize="none"
                autoCorrect="off"
                spellCheck={false}
              />
            </div>
            <div className="field">
              <Label htmlFor="ap-name">Display name</Label>
              <Input
                id="ap-name"
                value={newAP.name}
                onChange={(event) =>
                  setNewAP({ ...newAP, name: event.target.value })
                }
                placeholder="Reception AP"
              />
            </div>
            <SecretField
              id="ap-secret"
              required
              value={newAP.secret}
              onChange={(secret) => setNewAP({ ...newAP, secret })}
            />
            <div className="field">
              <Label htmlFor="ap-ip">
                Management IP <span className="subtle">(optional)</span>
              </Label>
              <Input
                id="ap-ip"
                value={newAP.management_ip}
                onChange={(event) =>
                  setNewAP({ ...newAP, management_ip: event.target.value })
                }
                placeholder="Any source address"
                inputMode="decimal"
                autoComplete="off"
                autoCapitalize="none"
                autoCorrect="off"
                spellCheck={false}
              />
              <span className="subtle">
                Optional: pin to a source IP. Leave blank to accept the AP from
                any address, including behind NAT.
              </span>
            </div>
            {createError && (
              <p className="inline-error" role="alert">
                {createError}
              </p>
            )}
            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                onClick={() => setCreateOpen(false)}
              >
                Cancel
              </Button>
              <Button type="submit" disabled={busy === "create"}>
                {busy === "create" ? "Registering…" : "Register AP"}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </section>
  );
}

// addressLabel shows where the AP is: its own LAN address and, when it differs,
// the public address its session arrives from.
export function addressLabel(ap: APEntry): string {
  const lan = ap.lan_ip || "";
  const remote = ap.remote_address || "";
  if (lan && remote && remote !== lan) return `${lan} via ${remote}`;
  return lan || remote || "Address not seen yet";
}
