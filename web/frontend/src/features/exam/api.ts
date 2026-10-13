import { request, ApiError } from "../../api/transport";
import {
  CreateExamRequest,
  CreateExamResponse,
  ExamDetailResponse,
  ResponseEventRequest,
  ResponseEventResponse,
  EventReceiptResponse,
  TelemetryGapsRequest,
  TelemetryGapsResponse,
  SubmitExamRequest,
  SubmitExamResponse,
  AbandonExamRequest,
  AbandonExamResponse,
  ExamResultResponse,
} from "./types";

export { ApiError };

export async function createExam(
  req: CreateExamRequest,
  idempotencyKey?: string
): Promise<CreateExamResponse> {
  const headers: Record<string, string> = {};
  if (idempotencyKey) {
    headers["Idempotency-Key"] = idempotencyKey;
  }
  return request("/api/v1/exams", {
    method: "POST",
    headers,
    body: JSON.stringify(req),
  });
}

export async function getExamDetail(examId: string): Promise<ExamDetailResponse> {
  return request(`/api/v1/exams/${examId}`, {
    method: "GET",
  });
}

export async function postResponseEvent(
  examId: string,
  event: ResponseEventRequest,
  idempotencyKey: string
): Promise<ResponseEventResponse> {
  return request(`/api/v1/exams/${examId}/response-events`, {
    method: "POST",
    headers: {
      "Idempotency-Key": idempotencyKey,
    },
    body: JSON.stringify(event),
  });
}

export async function getEventReceipt(
  examId: string,
  clientEventId: string
): Promise<EventReceiptResponse> {
  return request(`/api/v1/exams/${examId}/response-events/${clientEventId}`, {
    method: "GET",
  });
}

export async function postTelemetryGap(
  examId: string,
  req: TelemetryGapsRequest,
  idempotencyKey: string
): Promise<TelemetryGapsResponse> {
  return request(`/api/v1/exams/${examId}/telemetry-gaps`, {
    method: "POST",
    headers: {
      "Idempotency-Key": idempotencyKey,
    },
    body: JSON.stringify(req),
  });
}

export async function submitExam(
  examId: string,
  req: SubmitExamRequest,
  idempotencyKey: string
): Promise<SubmitExamResponse> {
  return request(`/api/v1/exams/${examId}/submit`, {
    method: "POST",
    headers: {
      "Idempotency-Key": idempotencyKey,
    },
    body: JSON.stringify(req),
  });
}

export async function abandonExam(
  examId: string,
  req: AbandonExamRequest,
  idempotencyKey?: string
): Promise<AbandonExamResponse> {
  const headers: Record<string, string> = {};
  if (idempotencyKey) {
    headers["Idempotency-Key"] = idempotencyKey;
  }
  return request(`/api/v1/exams/${examId}/abandon`, {
    method: "POST",
    headers,
    body: JSON.stringify(req),
  });
}

export async function getExamResult(examId: string): Promise<ExamResultResponse> {
  return request(`/api/v1/exams/${examId}/result`, {
    method: "GET",
  });
}

export async function getAvailableBanks(): Promise<{ items: Array<{ id: string; name: string; description?: string }> }> {
  return request("/api/v1/banks", {
    method: "GET",
  });
}
