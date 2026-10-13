export interface PublicQuestionOption {
  id: string;
  text: string;
}

export interface PublicQuestion {
  instance_id: string;
  ordinal: number;
  task_family: string;
  stem: string;
  options: PublicQuestionOption[];
}

export interface AttemptFeedback {
  attempt_id: string;
  correct: boolean;
  is_correct: boolean;
  correct_option_id: string;
  explanation: string;
  next_cursor: number;
  session_revision?: number;
}

export interface AcceptedAnswer {
  question: PublicQuestion;
  selected_option_id: string;
  feedback: AttemptFeedback;
}

export interface AnswerRecord {
  instance_id: string;
  ordinal: number;
  correct: boolean;
}

export interface PracticeSessionSummary {
  counts_scope: string;
  answered_count: number;
  correct_count: number;
  incorrect_count: number;
}

export interface PracticeSession {
  id: string;
  bank_id: string;
  status: "active" | "ended" | "open" | "completed" | "abandoned";
  cursor: number;
  revision: number;
}

export interface PracticeSessionDetail {
  session: PracticeSession;
  current_question?: PublicQuestion | null;
  last_answered_question?: AcceptedAnswer | null;
  summary: PracticeSessionSummary;
  recent_records: AnswerRecord[];
  has_older_records: boolean;
}

export interface NextQuestionResult {
  status: "ready" | "completed" | "unavailable";
  question?: PublicQuestion;
  reason?: string;
  counts?: any;
}

export interface AnswerRecordsResponse {
  items: AnswerRecord[];
  next_before_ordinal: number | null;
  has_more: boolean;
  summary: PracticeSessionSummary;
  session_revision: number;
}

export interface FeedbackViewRequest {
  client_event_id: string;
  instance_id: string;
  presentation: "history" | "restored_feedback";
  client_displayed_at: string;
}

export interface FeedbackViewResponse {
  view_id: string;
  client_event_id: string;
  event_sequence: number;
  recorded_at: string;
}
