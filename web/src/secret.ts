// AP secrets are the AP's 32-byte EEPROM secret. The API accepts them as hex
// (64 digits, optionally separated by spaces or colons) or base64 (standard or
// URL alphabet, padded or not). A secret.bin file is read in the browser and
// sent base64-encoded; it never leaves the page any other way.

export const SECRET_BYTES = 32;

function base64Bytes(value: string): number | null {
  let normalised = value.replace(/-/g, "+").replace(/_/g, "/");
  if (!/^[A-Za-z0-9+/]*={0,2}$/.test(normalised)) return null;
  normalised = normalised.replace(/=+$/, "");
  if (normalised.length % 4 === 1) return null;
  normalised += "=".repeat((4 - (normalised.length % 4)) % 4);
  try {
    return atob(normalised).length;
  } catch {
    return null;
  }
}

/** Returns a message when the pasted secret cannot be a 32-byte secret. */
export function secretProblem(value: string): string | null {
  const compact = value.replace(/[\s:]/g, "");
  if (!compact) return null;
  if (/^[0-9a-fA-F]+$/.test(compact) && compact.length === SECRET_BYTES * 2) {
    return null;
  }
  if (base64Bytes(compact) === SECRET_BYTES) return null;
  return "The secret must be 32 bytes: 64 hex digits or base64.";
}

/** Reads a secret.bin upload and returns it base64-encoded. */
export async function readSecretFile(file: File): Promise<string> {
  const bytes = new Uint8Array(await file.arrayBuffer());
  if (bytes.length !== SECRET_BYTES) {
    throw new Error(
      `${file.name} is ${bytes.length} bytes; an AP secret file is exactly ${SECRET_BYTES} bytes.`,
    );
  }
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary);
}
