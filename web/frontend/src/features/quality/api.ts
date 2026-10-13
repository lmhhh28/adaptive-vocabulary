import { request, ApiError } from "../../api/transport";
import {
  QualityFindingItem,
  PaginatedResponse,
  ResolveFindingRequest,
  ResolveFindingResponse,
  QuestionVersionItem,
  RetireRequest,
  RetireResponse,
  EvidenceDispositionRequest,
  EvidenceDispositionResponse,
  LearningSummaryResponse,
  SenseLearningDetail,
  MaintenanceStatus,
} from "./types";

export { ApiError };

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
  req: ResolveFindingRequest
): Promise<ResolveFindingResponse> {
  return request(`/api/v1/quality-findings/${id}/resolutions`, {
    method: "POST",
    body: JSON.stringify(req),
  });
}

export async function listQuestionVersions(params?: {
  bank_id?: string;
  cursor?: string;
  limit?: number;
}): Promise<PaginatedResponse<QuestionVersionItem>> {
  const query = new URLSearchParams();
  if (params?.bank_id) query.set("bank_id", params.bank_id);
  if (params?.cursor) query.set("cursor", params.cursor);
  if (params?.limit) query.set("limit", params.limit.toString());
  const qStr = query.toString();
  return request(`/api/v1/content/question-versions${qStr ? `?${qStr}` : ""}`, {
    method: "GET",
  });
}

export async function retireQuestionVersion(
  versionId: string,
  req: RetireRequest
): Promise<RetireResponse> {
  return request(`/api/v1/question-versions/${versionId}/retire`, {
    method: "POST",
    body: JSON.stringify(req),
  });
}

export async function applyEvidenceDisposition(
  versionId: string,
  req: EvidenceDispositionRequest,
  idempotencyKey?: string
): Promise<EvidenceDispositionResponse> {
  const headers: Record<string, string> = {};
  if (idempotencyKey) {
    headers["Idempotency-Key"] = idempotencyKey;
  }
  return request(`/api/v1/question-versions/${versionId}/evidence-dispositions`, {
    method: "POST",
    headers,
    body: JSON.stringify(req),
  });
}

export async function getLearningSummary(): Promise<LearningSummaryResponse> {
  return request("/api/v1/learning/summary", {
    method: "GET",
  });
}

export async function getLearningSenseDetail(senseId: string): Promise<SenseLearningDetail> {
  return request(`/api/v1/learning/senses/${senseId}`, {
    method: "GET",
  });
}

export async function getMaintenanceState(): Promise<MaintenanceStatus> {
  return request("/api/v1/maintenance", {
    method: "GET",
  });
}

export async function getActiveSession(): Promise<{ session: any | null }> {
  return request("/api/v1/sessions/active", {
    method: "GET",
  });
}
