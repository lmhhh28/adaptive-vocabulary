import { request, ApiError } from "../../api/transport";
import {
  BankOption,
  BankImportResponse,
  ImportJob,
  ImportItemsResponse,
  ImportItem,
  ResolveItemRequest,
} from "./types";

export { ApiError };

export async function fetchBanks(): Promise<{ items: BankOption[] }> {
  return request("/api/v1/banks", {
    method: "GET",
  });
}

export async function fetchBank(id: string): Promise<{ bank: BankOption }> {
  return request(`/api/v1/banks/${id}`, {
    method: "GET",
  });
}

export async function importBankVocabularyJson(
  bankId: string,
  payload: {
    words?: string[];
    text?: string;
    expected_revision: number;
    resolution_policy_version?: string;
  },
  idempotencyKey?: string
): Promise<BankImportResponse> {
  const headers: Record<string, string> = {};
  if (idempotencyKey) {
    headers["Idempotency-Key"] = idempotencyKey;
  }

  return request(`/api/v1/banks/${bankId}/imports`, {
    method: "POST",
    headers,
    body: JSON.stringify(payload),
  });
}

export async function importBankVocabularyFile(
  bankId: string,
  file: File,
  expectedRevision: number,
  resolutionPolicyVersion: string = "v1",
  idempotencyKey?: string
): Promise<BankImportResponse> {
  const form = new FormData();
  form.append("file", file);
  form.append("expected_revision", String(expectedRevision));
  form.append("resolution_policy_version", resolutionPolicyVersion);

  const headers: Record<string, string> = {};
  if (idempotencyKey) {
    headers["Idempotency-Key"] = idempotencyKey;
  }

  return request(`/api/v1/banks/${bankId}/imports`, {
    method: "POST",
    headers,
    body: form,
  });
}

export async function fetchImportJob(importId: string): Promise<ImportJob> {
  return request(`/api/v1/imports/${importId}`, {
    method: "GET",
  });
}

export async function fetchImportItems(
  importId: string,
  statusFilter?: string,
  limit: number = 50,
  offset: number = 0
): Promise<ImportItemsResponse> {
  const params = new URLSearchParams();
  if (statusFilter && statusFilter !== "all") {
    params.set("status", statusFilter);
  }
  params.set("limit", String(limit));
  params.set("offset", String(offset));

  return request(`/api/v1/imports/${importId}/items?${params.toString()}`, {
    method: "GET",
  });
}

export async function resolveImportItem(
  itemId: string,
  req: ResolveItemRequest,
  idempotencyKey?: string
): Promise<{ item: ImportItem; revision: number }> {
  const headers: Record<string, string> = {};
  if (idempotencyKey) {
    headers["Idempotency-Key"] = idempotencyKey;
  }

  return request(`/api/v1/import-items/${itemId}/resolve`, {
    method: "POST",
    headers,
    body: JSON.stringify(req),
  });
}
