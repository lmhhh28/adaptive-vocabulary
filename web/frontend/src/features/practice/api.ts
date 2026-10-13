import { request } from "../../api/transport";
import {
  PracticeSessionDetail,
  NextQuestionResult,
  AttemptFeedback,
  AnswerRecordsResponse,
  AcceptedAnswer,
  FeedbackViewRequest,
  FeedbackViewResponse,
} from "./types";

export function normalizeFeedback(fb: any): AttemptFeedback {
  if (!fb) return fb;
  const isCorrect = fb.correct !== undefined ? Boolean(fb.correct) : Boolean(fb.is_correct);
  return {
    ...fb,
    correct: isCorrect,
    is_correct: isCorrect,
    session_revision: fb.session_revision,
  };
}

export function normalizeAcceptedAnswer(ans: any): AcceptedAnswer {
  if (!ans) return ans;
  return {
    ...ans,
    feedback: normalizeFeedback(ans.feedback),
  };
}

export async function createPracticeSession(bankId: string): Promise<{ session: { id: string; bank_id: string; status: string } }> {
  return request("/api/v1/practice-sessions", {
    method: "POST",
    body: JSON.stringify({ bank_id: bankId }),
  });
}

export async function getPracticeSession(sessionId: string): Promise<PracticeSessionDetail> {
  const detail = await request<any>(`/api/v1/practice-sessions/${sessionId}`, {
    method: "GET",
  });
  if (detail.last_answered_question) {
    detail.last_answered_question = normalizeAcceptedAnswer(detail.last_answered_question);
  }
  if (detail.last_feedback) {
    detail.last_feedback = normalizeFeedback(detail.last_feedback);
  }
  return detail;
}

export async function fetchNextQuestion(
  sessionId: string,
  expectedCursor: number
): Promise<NextQuestionResult> {
  return request(`/api/v1/practice-sessions/${sessionId}/next`, {
    method: "POST",
    body: JSON.stringify({ expected_cursor: expectedCursor }),
  });
}

export async function submitAttempt(
  sessionId: string,
  instanceId: string,
  optionId: string,
  idempotencyKey: string
): Promise<AttemptFeedback> {
  const res = await request<any>(`/api/v1/practice-sessions/${sessionId}/attempts`, {
    method: "POST",
    headers: {
      "Idempotency-Key": idempotencyKey,
    },
    body: JSON.stringify({
      instance_id: instanceId,
      option_id: optionId,
    }),
  });
  return normalizeFeedback(res);
}

export async function getAnswerRecords(
  sessionId: string,
  beforeOrdinal?: number,
  limit = 30
): Promise<AnswerRecordsResponse> {
  let query = `limit=${limit}`;
  if (beforeOrdinal !== undefined) {
    query += `&before_ordinal=${beforeOrdinal}`;
  }
  return request(`/api/v1/practice-sessions/${sessionId}/answer-records?${query}`, {
    method: "GET",
  });
}

export async function getAnsweredQuestion(
  sessionId: string,
  ordinal: number
): Promise<AcceptedAnswer> {
  const res = await request<any>(`/api/v1/practice-sessions/${sessionId}/answered-questions/${ordinal}`, {
    method: "GET",
  });
  return normalizeAcceptedAnswer(res);
}

export async function submitFeedbackView(
  sessionId: string,
  payload: FeedbackViewRequest
): Promise<FeedbackViewResponse> {
  return request(`/api/v1/practice-sessions/${sessionId}/feedback-views`, {
    method: "POST",
    headers: {
      "Idempotency-Key": payload.client_event_id,
    },
    body: JSON.stringify(payload),
  });
}

export async function endPracticeSession(
  sessionId: string,
  expectedRevision = 0,
  reason = "ended"
): Promise<{ session: { id: string; status: string } }> {
  return request(`/api/v1/practice-sessions/${sessionId}/end`, {
    method: "POST",
    body: JSON.stringify({
      expected_revision: expectedRevision,
      reason: reason || "ended",
    }),
  });
}

export async function getAvailableBanks(): Promise<{ items: Array<{ id: string; name: string; description?: string }> }> {
  return request("/api/v1/banks", {
    method: "GET",
  });
}
