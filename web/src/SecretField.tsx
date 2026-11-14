import { useRef, useState } from "react";
import { Upload } from "lucide-react";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import { Label } from "./components/ui/label";
import { writeOnlySecret } from "./lib/utils";
import { readSecretFile, secretProblem } from "./secret";

type Props = {
  id: string;
  value: string;
  onChange: (value: string) => void;
  required?: boolean;
  /** Edit mode: blank keeps the stored secret. */
  hasSecret?: boolean;
};

// SecretField takes the AP's EEPROM secret as a hex/base64 paste or a
// secret.bin upload (read here and base64-encoded).
export default function SecretField({
  id,
  value,
  onChange,
  required,
  hasSecret,
}: Props) {
  const fileInput = useRef<HTMLInputElement>(null);
  const [fileName, setFileName] = useState("");
  const [fileError, setFileError] = useState("");
  const problem = fileError || secretProblem(value);
  const editing = hasSecret !== undefined;

  async function onFile(file: File | undefined) {
    setFileError("");
    setFileName("");
    if (!file) return;
    try {
      onChange(await readSecretFile(file));
      setFileName(file.name);
    } catch (cause) {
      onChange("");
      setFileError(
        cause instanceof Error ? cause.message : "Could not read that file",
      );
    } finally {
      if (fileInput.current) fileInput.current.value = "";
    }
  }

  return (
    <div className="field">
      <Label htmlFor={id}>
        {editing ? "New AP secret" : "AP secret"}
        {editing && <span className="subtle"> (optional)</span>}
      </Label>
      <div className="flex gap-2">
        <Input
          id={id}
          type="password"
          required={required}
          value={value}
          onChange={(event) => {
            setFileName("");
            setFileError("");
            onChange(event.target.value);
          }}
          placeholder={
            editing
              ? "Leave blank to keep the stored secret"
              : "64 hex digits or base64"
          }
          {...writeOnlySecret}
          autoCapitalize="none"
          autoCorrect="off"
          spellCheck={false}
          aria-invalid={problem ? true : undefined}
          aria-describedby={`${id}-help`}
        />
        <Button
          type="button"
          variant="outline"
          onClick={() => fileInput.current?.click()}
        >
          <Upload size={14} /> secret.bin
        </Button>
        <input
          ref={fileInput}
          type="file"
          className="sr-only"
          tabIndex={-1}
          aria-hidden="true"
          onChange={(event) => void onFile(event.target.files?.[0])}
        />
      </div>
      <span className="subtle" id={`${id}-help`}>
        {fileName
          ? `Loaded ${fileName} (32 bytes). It is sent to this controller only.`
          : editing
            ? hasSecret
              ? "A secret is stored for this AP. Paste or upload a new one to replace it."
              : "No secret is stored for this AP yet: it cannot connect until you add one."
            : "Paste the AP's 32-byte EEPROM secret, or upload its secret.bin."}
      </span>
      {problem && (
        <span className="text-xs text-destructive" role="alert">
          {problem}
        </span>
      )}
    </div>
  );
}
