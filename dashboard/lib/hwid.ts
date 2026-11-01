// Browser-side device fingerprint for the license HWID gate.
//
// Combines a persistent localStorage UUID + the stable browser/screen
// signals into a hash. Users who clear browser data regenerate a new
// HWID — they'll have to use the 24h reset flow once per clear.

const STORAGE_KEY = "memeforge_device_id";

function getOrCreateDeviceID(): string {
  try {
    let id = localStorage.getItem(STORAGE_KEY);
    if (!id) {
      id = crypto.randomUUID();
      localStorage.setItem(STORAGE_KEY, id);
    }
    return id;
  } catch {
    // Private mode / disabled storage. Fall back to a session-only ID
    // so the user can still log in; they'll trip the reset flow on
    // every reload, but that's better than being locked out entirely.
    return "ephemeral-" + crypto.randomUUID();
  }
}

async function sha256Hex(s: string): Promise<string> {
  const bytes = new TextEncoder().encode(s);
  const digest = await crypto.subtle.digest("SHA-256", bytes);
  return Array.from(new Uint8Array(digest))
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
}

export async function computeHWID(): Promise<string> {
  const deviceID = getOrCreateDeviceID();
  const parts = [
    deviceID,
    navigator.userAgent,
    screen.width + "x" + screen.height,
    navigator.language,
    new Date().getTimezoneOffset().toString(),
    navigator.hardwareConcurrency?.toString() ?? "",
  ];
  return sha256Hex(parts.join("|"));
}
