import { fetchClientContext, getCachedCsrfToken } from "./clientContext";

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public details?: any
  ) {
    super(message);
    this.name = "ApiError";
  }
}

const DEFAULT_TIMEOUT_MS = 20000;

function createTimeoutSignal(
  ms: number,
  parentSignal?: AbortSignal | null
): { signal: AbortSignal; cleanup: () => void } {
  const controller = new AbortController();
  const timer = setTimeout(() => {
    controller.abort(new Error(`Request timeout after ${ms}ms`));
  }, ms);

  let onParentAbort: (() => void) | undefined;
  if (parentSignal) {
    if (parentSignal.aborted) {
      controller.abort(parentSignal.reason);
    } else {
      onParentAbort = () => controller.abort(parentSignal.reason);
      parentSignal.addEventListener("abort", onParentAbort);
    }
  }

  const cleanup = () => {
    clearTimeout(timer);
    if (parentSignal && onParentAbort) {
      parentSignal.removeEventListener("abort", onParentAbort);
    }
  };

  return { signal: controller.signal, cleanup };
}

async function parseJsonResponse<T>(response: Response): Promise<T> {
  try {
    return await response.json();
  } catch (err: any) {
    throw new ApiError(
      response.status,
      "INVALID_RESPONSE",
      `服务器返回了非预期格式的数据 (Non-JSON): ${err?.message || "SyntaxError"}`
    );
  }
}

export async function request<T = any>(path: string, options: RequestInit = {}): Promise<T> {
  const method = (options.method || "GET").toUpperCase();
  const headers = new Headers(options.headers || {});
  headers.set("Accept", "application/json");

  if (method !== "GET" && method !== "HEAD") {
    if (!headers.has("Content-Type") && options.body && !(options.body instanceof FormData)) {
      headers.set("Content-Type", "application/json");
    }
    let csrfToken = getCachedCsrfToken();
    if (!csrfToken) {
      const ctx = await fetchClientContext();
      csrfToken = ctx.csrf_token;
    }
    if (csrfToken) {
      headers.set("X-CSRF-Token", csrfToken);
    }
  }

  const { signal, cleanup } = createTimeoutSignal(DEFAULT_TIMEOUT_MS, options.signal);

  try {
    const response = await fetch(path, {
      ...options,
      headers,
      credentials: "same-origin",
      signal,
    });

    if (response.status === 403) {
      // Check if CSRF token was rejected
      try {
        const clone = response.clone();
        const errBody = await clone.json();
        if (errBody?.error?.code === "CSRF_REJECTED" || errBody?.error?.code === "INVALID_CSRF") {
          // Refresh context and retry once
          const freshCtx = await fetchClientContext(true);
          headers.set("X-CSRF-Token", freshCtx.csrf_token);
          const retryResponse = await fetch(path, {
            ...options,
            headers,
            credentials: "same-origin",
            signal,
          });
          if (!retryResponse.ok) {
            return handleResponseError(retryResponse);
          }
          if (retryResponse.status === 204) {
            return {} as T;
          }
          return parseJsonResponse<T>(retryResponse);
        }
      } catch {
        // Continue to regular error handler
      }
    }

    if (!response.ok) {
      return handleResponseError(response);
    }

    if (response.status === 204) {
      return {} as T;
    }
    return parseJsonResponse<T>(response);
  } catch (err: any) {
    if (err instanceof ApiError) {
      throw err;
    }
    if (err.name === "AbortError" || signal.aborted) {
      throw new ApiError(0, "REQUEST_TIMEOUT", "网络请求超时或已被取消，请检查网络连接");
    }
    throw new ApiError(0, "NETWORK_ERROR", err.message || "网络请求失败");
  } finally {
    cleanup();
  }
}

async function handleResponseError(response: Response): Promise<never> {
  let code = "UNKNOWN_ERROR";
  let message = `Request failed with status ${response.status}`;
  let details = null;
  try {
    const body = await response.json();
    if (body.error) {
      code = body.error.code || code;
      message = body.error.message || message;
      details = body.error.details || null;
    }
  } catch {
    // Non-JSON response
  }
  throw new ApiError(response.status, code, message, details);
}
