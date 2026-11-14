import { useState, type FormEvent } from "react";
import { Pencil } from "lucide-react";
import { api } from "./api";
import type { APEntry } from "./APInventory";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import { Label } from "./components/ui/label";
import SecretField from "./SecretField";
import { secretProblem } from "./secret";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "./components/ui/dialog";

type Props = { ap: APEntry; onSaved: (id: string) => void | Promise<void> };
type Form = {
  name: string;
  management_ip: string;
  secret: string;
};

function initialForm(ap: APEntry): Form {
  return {
    name: ap.name || "",
    management_ip: ap.management_ip || "",
    secret: "",
  };
}

export default function APEditor({ ap, onSaved }: Props) {
  const [open, setOpen] = useState(false);
  const [form, setForm] = useState<Form>(() => initialForm(ap));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  function onOpenChange(next: boolean) {
    if (busy) return;
    setOpen(next);
    if (next) {
      setForm(initialForm(ap));
      setError("");
    }
  }

  async function save(event: FormEvent) {
    event.preventDefault();
    const problem = secretProblem(form.secret);
    if (problem) {
      setError(problem);
      return;
    }
    setBusy(true);
    setError("");
    try {
      // A blank secret is left out, so the stored secret is kept. A blank
      // management IP removes the source pin.
      const body: Record<string, string> = {
        name: form.name.trim(),
        management_ip: form.management_ip.trim(),
      };
      if (form.secret.trim()) body.secret = form.secret.trim();
      await api(`/api/aps/${encodeURIComponent(ap.id)}`, {
        method: "PUT",
        body: JSON.stringify(body),
      });
      await onSaved(ap.id);
      setForm({ ...form, secret: "" });
      setOpen(false);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Could not update AP");
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <Button variant="outline" size="sm" onClick={() => onOpenChange(true)}>
        <Pencil size={14} /> Edit AP
      </Button>
      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>Edit access point</DialogTitle>
            <DialogDescription>
              Rename this AP, pin it to a source address or replace its secret.
              Model and firmware come from the AP itself.
            </DialogDescription>
          </DialogHeader>
          <form onSubmit={save} className="register-form">
            <div className="field">
              <Label htmlFor="edit-ap-name">Display name</Label>
              <Input
                id="edit-ap-name"
                value={form.name}
                onChange={(event) =>
                  setForm({ ...form, name: event.target.value })
                }
                placeholder="Reception AP"
              />
            </div>
            <div className="field">
              <span className="text-sm font-medium">Model</span>
              <span className="subtle">
                {ap.model || "Not reported yet"} · reported by the AP
              </span>
            </div>
            <div className="field">
              <Label htmlFor="edit-ap-ip">
                Management IP <span className="subtle">(optional)</span>
              </Label>
              <Input
                id="edit-ap-ip"
                value={form.management_ip}
                onChange={(event) =>
                  setForm({ ...form, management_ip: event.target.value })
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
            <SecretField
              id="edit-ap-secret"
              value={form.secret}
              hasSecret={Boolean(ap.has_secret)}
              onChange={(secret) => setForm({ ...form, secret })}
            />
            {error && (
              <p className="inline-error" role="alert">
                {error}
              </p>
            )}
            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                disabled={busy}
                onClick={() => onOpenChange(false)}
              >
                Cancel
              </Button>
              <Button type="submit" disabled={busy}>
                {busy ? "Saving…" : "Save changes"}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </>
  );
}
