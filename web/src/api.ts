export class UnauthorizedError extends Error {
  constructor() {
    super("Your session has ended. Sign in to continue.");
  }
}

export async function api<T>(
  path: string,
  options: RequestInit = {},
): Promise<T> {
  const response = await fetch(path, {
    cache: "no-store",
    credentials: "same-origin",
    ...options,
    headers: { "Content-Type": "application/json", ...options.headers },
  });
  if (response.status === 401) {
    window.dispatchEvent(new Event("controller:unauthorized"));
    throw new UnauthorizedError();
  }
  if (!response.ok) {
    throw new Error(await errorMessage(response));
  }
  return response.json() as Promise<T>;
}

// errorMessage turns an error response into one sentence: the JSON contract
// {error, code, fields} when the server sends it, otherwise the plain text.
async function errorMessage(response: Response): Promise<string> {
  const text = (await response.text()).trim();
  try {
    const body = JSON.parse(text) as {
      error?: unknown;
      fields?: Record<string, unknown>;
    };
    const fields = Object.values(body.fields ?? {}).filter(
      (value): value is string => typeof value === "string",
    );
    if (typeof body.error === "string" && body.error) {
      return [body.error, ...fields.filter((f) => f !== body.error)].join(" ");
    }
    if (fields.length) return fields.join(" ");
  } catch {
    // Not JSON: fall through to the text.
  }
  return text || `Request failed (${response.status})`;
}

export function timeLabel(value?: string): string {
  if (!value || value.startsWith("0001-")) return "Unavailable";
  const date = new Date(value);
  return Number.isNaN(date.getTime())
    ? "Unavailable"
    : date.toLocaleString(undefined, {
        dateStyle: "medium",
        timeStyle: "short",
      });
}

export function bytesLabel(value?: number): string {
  if (value == null || !Number.isFinite(value)) return "Unavailable";
  if (value < 1024) return `${value} B`;
  if (value < 1024 ** 2) return `${(value / 1024).toFixed(1)} KB`;
  if (value < 1024 ** 3) return `${(value / 1024 ** 2).toFixed(1)} MB`;
  return `${(value / 1024 ** 3).toFixed(1)} GB`;
}

export function durationLabel(seconds?: number): string {
  if (seconds == null || !Number.isFinite(seconds)) return "Unavailable";
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  return [days ? `${days}d` : "", hours ? `${hours}h` : "", `${minutes}m`]
    .filter(Boolean)
    .join(" ");
}
