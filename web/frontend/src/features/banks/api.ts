import { request, ApiError } from "../../api/transport";
import {
  BankSummary,
  BankDetail,
  Source,
  SenseVersionItem,
  QuestionVersionItem,
  CreateBankRequest,
  CreateSourceRequest,
  PaginatedResponse,
} from "./types";

export { ApiError };

export async function listBanks(): Promise<{ items: BankSummary[] }> {
  return request("/api/v1/banks", {
    method: "GET",
  });
}

export async function getBankDetail(id: string): Promise<{ bank: BankDetail }> {
  return request(`/api/v1/banks/${id}`, {
    method: "GET",
  });
}

export async function createBank(
  req: CreateBankRequest,
  idempotencyKey?: string
): Promise<{ bank_id: string; revision: number }> {
  const headers: Record<string, string> = {};
  if (idempotencyKey) {
    headers["Idempotency-Key"] = idempotencyKey;
  }
  return request("/api/v1/banks", {
    method: "POST",
    headers,
    body: JSON.stringify(req),
  });
}

export async function archiveBank(
  id: string,
  expectedRevision: number,
  idempotencyKey?: string
): Promise<{ bank_id: string; archived: boolean; revision: number }> {
  const headers: Record<string, string> = {};
  if (idempotencyKey) {
    headers["Idempotency-Key"] = idempotencyKey;
  }
  return request(`/api/v1/banks/${id}/archive`, {
    method: "POST",
    headers,
    body: JSON.stringify({ expected_revision: expectedRevision }),
  });
}

export async function removeBankMembers(
  id: string,
  senseIds: string[],
  expectedRevision: number,
  idempotencyKey?: string
): Promise<{ bank_id: string; revision: number; removed_count: number }> {
  const headers: Record<string, string> = {};
  if (idempotencyKey) {
    headers["Idempotency-Key"] = idempotencyKey;
  }
  return request(`/api/v1/banks/${id}/members/remove`, {
    method: "POST",
    headers,
    body: JSON.stringify({
      expected_revision: expectedRevision,
      sense_ids: senseIds,
    }),
  });
}

export async function listSources(): Promise<{ items: Source[] }> {
  return request("/api/v1/sources", {
    method: "GET",
  });
}

export async function createSource(
  req: CreateSourceRequest,
  idempotencyKey?: string
): Promise<{ source_id: string; revision: number }> {
  const headers: Record<string, string> = {};
  if (idempotencyKey) {
    headers["Idempotency-Key"] = idempotencyKey;
  }
  return request("/api/v1/sources", {
    method: "POST",
    headers,
    body: JSON.stringify(req),
  });
}

export async function listSenseVersions(params?: {
  bank_id?: string;
  cursor?: string;
  limit?: number;
}): Promise<PaginatedResponse<SenseVersionItem>> {
  const searchParams = new URLSearchParams();
  if (params?.bank_id) searchParams.set("bank_id", params.bank_id);
  if (params?.cursor) searchParams.set("cursor", params.cursor);
  if (params?.limit) searchParams.set("limit", String(params.limit));

  const queryStr = searchParams.toString();
  const url = queryStr ? `/api/v1/content/sense-versions?${queryStr}` : "/api/v1/content/sense-versions";
  return request(url, {
    method: "GET",
  });
}

export async function listQuestionVersions(params?: {
  bank_id?: string;
  cursor?: string;
  limit?: number;
}): Promise<PaginatedResponse<QuestionVersionItem>> {
  const searchParams = new URLSearchParams();
  if (params?.bank_id) searchParams.set("bank_id", params.bank_id);
  if (params?.cursor) searchParams.set("cursor", params.cursor);
  if (params?.limit) searchParams.set("limit", String(params.limit));

  const queryStr = searchParams.toString();
  const url = queryStr ? `/api/v1/content/question-versions?${queryStr}` : "/api/v1/content/question-versions";
  return request(url, {
    method: "GET",
  });
}

export async function getActiveSession(): Promise<{ session: any | null }> {
  return request("/api/v1/sessions/active", {
    method: "GET",
  });
}
