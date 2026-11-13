import { useState, type FormEvent } from "react";
import { Pencil } from "lucide-react";
import { api } from "./api";
import type { APEntry } from "./APInventory";
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

type Props = { ap: APEntry; onSaved: (id: string) => void | Promise<void> };
type Form = {
  name: string;
  model: string;
  management_ip: string;
  secret_file: string;
};

export default function APEditor({ ap, onSaved }: Props) {
  const [open, setOpen] = useState(false);
  const [form, setForm] = useState<Form>({
    name: ap.name || "",
    model: ap.model || "",
    management_ip: ap.management_ip || "",
    secret_file: "",
  });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  function onOpenChange(next: boolean) {
    if (busy) return;
    setOpen(next);
    if (next) {
      setForm({
        name: ap.name || "",
        model: ap.model || "",
        management_ip: ap.management_ip || "",
        secret_file: "",
      });
      setError("");
    }
  }

  async function save(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      const body: Record<string, string> = {
        name: form.name.trim(),
        model: form.model.trim(),
        management_ip: form.management_ip.trim(),
      };
      if (form.secret_file.trim()) body.secret_file = form.secret_file.trim();
      await api(`/api/aps/${encodeURIComponent(ap.id)}`, {
        method: "PUT",
        body: JSON.stringify(body),
      });
      await onSaved(ap.id);
      setForm({ ...form, secret_file: "" });
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
              Update this AP’s identity and management address. Changes are
              saved on this controller.
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
              <Label htmlFor="edit-ap-model">Model</Label>
              <Input
                id="edit-ap-model"
                value={form.model}
                onChange={(event) =>
                  setForm({ ...form, model: event.target.value })
                }
                placeholder="AP model"
              />
            </div>
            <div className="field">
              <Label htmlFor="edit-ap-ip">Management IP</Label>
              <Input
                id="edit-ap-ip"
                required
                value={form.management_ip}
                onChange={(event) =>
                  setForm({ ...form, management_ip: event.target.value })
                }
                placeholder="192.0.2.10"
              />
            </div>
            <div className="field">
              <Label htmlFor="edit-ap-secret">
                New server secret file path
              </Label>
              <Input
                id="edit-ap-secret"
                value={form.secret_file}
                onChange={(event) =>
                  setForm({ ...form, secret_file: event.target.value })
                }
                placeholder="Leave blank to retain existing secret"
                autoComplete="off"
              />
              <span className="subtle">
                Existing secret file remains in use when this is blank. Its path
                is not shown here.
              </span>
            </div>
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
