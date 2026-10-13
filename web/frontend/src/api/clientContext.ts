export interface ClientContext {
  csrf_token: string;
  active_sessions?: Array<{ id: string; mode: string; bank_id?: string }>;
}

let cachedContext: ClientContext | null = null;

export async function fetchClientContext(force = false): Promise<ClientContext> {
  if (cachedContext && !force) {
    return cachedContext;
  }
  const response = await fetch("/client-context", {
    method: "GET",
    credentials: "same-origin",
    headers: {
      "Accept": "application/json",
    },
  });
  if (!response.ok) {
    throw new Error(`Failed to fetch client context: ${response.status} ${response.statusText}`);
  }
  const data = await response.json();
  cachedContext = data;
  return data;
}

export function getCachedCsrfToken(): string | null {
  return cachedContext?.csrf_token ?? null;
}
