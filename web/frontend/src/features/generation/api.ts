import { request, ApiError } from "../../api/transport";
import {
  JobSummary,
  CreateJobRequest,
  CreateJobResponse,
  CandidateDetailResponse,
  QualityFindingItem,
  PaginatedResponse,
  BankOption,
  SenseVersionItem,
} from "./types";

export { ApiError };

export async function listGenerationJobs(params?: {
  bank_id?: string;
  cursor?: string;
  limit?: number;
}): Promise<PaginatedResponse<JobSummary>> {
  const query = new URLSearchParams();
  if (params?.bank_id) query.set("bank_id", params.bank_id);
  if (params?.cursor) query.set("cursor", params.cursor);
  if (params?.limit) query.set("limit", params.limit.toString());
  const qStr = query.toString();
  return request(`/api/v1/generation-jobs${qStr ? `?${qStr}` : ""}`, {
    method: "GET",
  });
}

export async function getGenerationJob(id: string): Promise<JobSummary> {
  return request(`/api/v1/generation-jobs/${id}`, {
    method: "GET",
  });
}

export async function createGenerationJob(
  bankId: string,
  req: CreateJobRequest,
  idempotencyKey?: string
): Promise<CreateJobResponse> {
  const headers: Record<string, string> = {};
  if (idempotencyKey) {
    headers["Idempotency-Key"] = idempotencyKey;
  }
  return request(`/api/v1/banks/${bankId}/generation-jobs`, {
    method: "POST",
    headers,
    body: JSON.stringify(req),
  });
}

export async function getCandidateDetail(id: string): Promise<CandidateDetailResponse> {
  return request(`/api/v1/question-candidates/${id}`, {
    method: "GET",
  });
}

export async function listQualityFindings(params?: {
  job_id?: string;
  candidate_id?: string;
  status?: string;
  cursor?: string;
  limit?: number;
}): Promise<PaginatedResponse<QualityFindingItem>> {
  const query = new URLSearchParams();
  if (params?.job_id) query.set("job_id", params.job_id);
  if (params?.candidate_id) query.set("candidate_id", params.candidate_id);
  if (params?.status) query.set("status", params.status);
  if (params?.cursor) query.set("cursor", params.cursor);
  if (params?.limit) query.set("limit", params.limit.toString());
  const qStr = query.toString();
  return request(`/api/v1/quality-findings${qStr ? `?${qStr}` : ""}`, {
    method: "GET",
  });
}

export async function resolveQualityFinding(
  id: string,
  req: { action: string; reason: string; expected_revision: number }
): Promise<{ resolution_id: string; status: string; revision: number }> {
  return request(`/api/v1/quality-findings/${id}/resolutions`, {
    method: "POST",
    body: JSON.stringify(req),
  });
}

export async function listBanks(): Promise<{ items: BankOption[] }> {
  return request("/api/v1/banks", {
    method: "GET",
  });
}

export async function listSenseVersions(params?: {
  bank_id?: string;
  cursor?: string;
  limit?: number;
}): Promise<PaginatedResponse<SenseVersionItem>> {
  const query = new URLSearchParams();
  if (params?.bank_id) query.set("bank_id", params.bank_id);
  if (params?.cursor) query.set("cursor", params.cursor);
  if (params?.limit) query.set("limit", params.limit.toString());
  const qStr = query.toString();
  return request(`/api/v1/content/sense-versions${qStr ? `?${qStr}` : ""}`, {
    method: "GET",
  });
}
