import { api } from "./api";
import { useState, type FormEvent } from "react";

export type Site = {
  id: string;
  name: string;
  country: string;
  timezone: string;
  ap_count: number;
  network_count: number;
};

type Props = {
  sites: Site[];
  selectedID: string;
  onSelect: (id: string) => void;
  onCreated: (id: string) => void;
};

export default function SiteBar({
  sites,
  selectedID,
  onSelect,
  onCreated,
}: Props) {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [country, setCountry] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function create(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      const data = await api<{ site: Site }>("/api/sites", {
        method: "POST",
        body: JSON.stringify({ name: name.trim(), country }),
      });
      setOpen(false);
      setName("");
      onCreated(data.site.id);
    } catch (cause) {
      setError(
        cause instanceof Error ? cause.message : "Could not create site",
      );
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="site-bar">
      <div className="site-selector">
        <span className="section-kicker">Current site</span>
        <select
          aria-label="Current site"
          value={selectedID}
          onChange={(event) => onSelect(event.target.value)}
        >
          {sites.map((site) => (
            <option value={site.id} key={site.id}>
              {site.name}
            </option>
          ))}
        </select>
      </div>
      <div className="site-counts">
        {sites.find((site) => site.id === selectedID)?.ap_count ?? 0}{" "}
        {sites.find((site) => site.id === selectedID)?.ap_count === 1
          ? "AP"
          : "APs"}{" "}
        · {sites.find((site) => site.id === selectedID)?.network_count ?? 0}{" "}
        {sites.find((site) => site.id === selectedID)?.network_count === 1
          ? "WLAN"
          : "WLANs"}
      </div>
      <button
        className="secondary-button"
        type="button"
        onClick={() => setOpen(!open)}
      >
        {open ? "Cancel" : "New site"}
      </button>
      {open && (
        <form className="site-create" onSubmit={(event) => void create(event)}>
          <label>
            Site name
            <input
              required
              maxLength={80}
              value={name}
              onChange={(event) => setName(event.target.value)}
              placeholder="Main office"
            />
          </label>
          <label>
            Country
            <input
              required
              maxLength={2}
              value={country}
              onChange={(event) => setCountry(event.target.value.toUpperCase())}
              placeholder="AU"
            />
          </label>
          <button className="primary-button" type="submit" disabled={busy}>
            {busy ? "Creating…" : "Create site"}
          </button>
          {error && (
            <span role="alert" className="site-error">
              {error}
            </span>
          )}
        </form>
      )}
    </div>
  );
}
