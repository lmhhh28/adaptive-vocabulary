/** Browser-only, same-origin JSON API example. No automatic write retries. */
export class ApiError extends Error {
  constructor(response, payload, fallback) {
    super(payload?.error?.message ?? fallback);
    this.name = 'ApiError';
    this.status = response.status;
    this.code = payload?.error?.code ?? 'HTTP_ERROR';
    this.details = payload?.error?.details ?? {};
    this.requestId = payload?.error?.request_id ?? response.headers.get('X-Request-Id');
  }
}

let csrfToken;
let contextPromise;

async function parseResponse(response) {
  const text = await response.text();
  let payload;
  try { payload = JSON.parse(text); } catch { /* Unknown routes may return text/HTML. */ }
  if (!response.ok) throw new ApiError(response, payload, `HTTP ${response.status}`);
  if (payload === undefined) throw new ApiError(response, null, 'Expected a JSON API response');
  return {
    data: payload,
    status: response.status,
    replayed: response.headers.get('Idempotent-Replayed') === 'true',
    requestId: response.headers.get('X-Request-Id')
  };
}

export async function initializeContext({ refresh = false } = {}) {
  if (refresh) csrfToken = undefined;
  if (csrfToken) return csrfToken;
  if (!contextPromise) {
    contextPromise = (async () => {
      const response = await fetch('/client-context', {
        credentials: 'same-origin', cache: 'no-store', headers: { Accept: 'application/json' }
      });
      const { data } = await parseResponse(response);
      if (typeof data.csrf_token !== 'string' || !data.csrf_token) throw new Error('Missing CSRF token');
      csrfToken = data.csrf_token;
      return csrfToken;
    })().finally(() => { contextPromise = undefined; });
  }
  return contextPromise;
}

function requireAPIPath(path) {
  if (typeof path !== 'string' || !path.startsWith('/api/v1/') || path.includes('://')) {
    throw new TypeError('Use a same-origin /api/v1/ path');
  }
}

export async function getJSON(path, { signal } = {}) {
  requireAPIPath(path);
  return parseResponse(await fetch(path, {
    credentials: 'same-origin', cache: 'no-store', signal, headers: { Accept: 'application/json' }
  }));
}

// Create once per user action. Store this object, not just a newly generated key.
export function prepareWrite(path, body, key) {
  requireAPIPath(path);
  const bodyID = body?.client_event_id ?? body?.submission_id;
  const operationKey = key ?? bodyID ?? crypto.randomUUID();
  if (bodyID && bodyID !== operationKey) throw new TypeError('Body ID must match Idempotency-Key');
  const serialized = JSON.stringify(body);
  if (serialized === undefined) throw new TypeError('A JSON request body is required');
  return Object.freeze({ path, key: operationKey, body: serialized });
}

// Retrying means sending the same prepared object; this function never retries itself.
// Sending a key does NOT add server-side idempotency to abandon/retire/finding resolutions.
export async function sendPrepared(operation, { signal } = {}) {
  requireAPIPath(operation.path);
  if (typeof operation.key !== 'string' || !operation.key || typeof operation.body !== 'string') {
    throw new TypeError('Invalid prepared operation');
  }
  const token = await initializeContext();
  return parseResponse(await fetch(operation.path, {
    method: 'POST', credentials: 'same-origin', cache: 'no-store', signal,
    headers: { Accept: 'application/json', 'Content-Type': 'application/json',
               'X-CSRF-Token': token, 'Idempotency-Key': operation.key },
    body: operation.body
  }));
}
