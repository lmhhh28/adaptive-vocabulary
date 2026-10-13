/**
 * Cross-environment safe UUID generator.
 * In non-secure contexts (e.g. HTTP on local network IP), window.crypto.randomUUID is undefined.
 * This function falls back to a RFC4122-compliant pseudo-random UUID.
 */
export function getSecureRandomUUID(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    try {
      return crypto.randomUUID();
    } catch {
      // In case of restrictive browser security policy
    }
  }

  // RFC4122 version 4 compliant fallback
  return "xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx".replace(/[xy]/g, (c) => {
    const r = (Math.random() * 16) | 0;
    const v = c === "x" ? r : (r & 0x3) | 0x8;
    return v.toString(16);
  });
}
