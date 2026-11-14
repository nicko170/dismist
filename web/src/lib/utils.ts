export { cn } from "cn"

// Attributes for a write-only secret input (an AP secret, a WLAN
// passphrase): "new-password" stops browsers filling in the saved admin
// token (a 64-hex-digit value that passes as an AP secret), and the data-*
// attributes keep 1Password, LastPass, Bitwarden and Dashlane away too
// (design §7.7 rule 6).
export const writeOnlySecret = {
  autoComplete: "new-password",
  "data-1p-ignore": "true",
  "data-lpignore": "true",
  "data-bwignore": "true",
  "data-form-type": "other",
} as const;
