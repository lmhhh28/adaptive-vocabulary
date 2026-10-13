export interface PublicOption {
  id: string;
  text: string;
}

export interface PublicQuestion {
  instance_id: string;
  ordinal: number;
  task_family: string;
  stem: string;
  options: PublicOption[];
}

export interface ExamDraft {
  instance_id: string;
  selected_option_id: string | null;
  revision_count: number;
}

export interface CreateExamRequest {
  bank_id: string;
  question_count: number;
  task_quotas?: Record<string, number>;
  time_limit_seconds?: number;
  tags?: string[];
}

export interface CreateExamResponse {
  exam_id: string;
  session_revision: number;
  deadline?: string;
  public_questions: PublicQuestion[];
  drafts: ExamDraft[];
  last_ack_sequence: number;
}

export interface ExamDetailResponse {
  id: string;
  status: "open" | "submitted" | "abandoned" | string;
  session_revision: number;
  deadline?: string;
  public_questions: PublicQuestion[];
  drafts: ExamDraft[];
  last_ack_sequence: number;
  result_url?: string;
}

export interface RebasedFromDTO {
  client_sequence: number;
  expected_revision: number;
}

export interface ResponseEventRequest {
  instance_id: string;
  action: "select" | "clear";
  option_id?: string;
  client_event_id: string;
  client_sequence: number;
  expected_revision: number;
  rebased_from?: RebasedFromDTO;
}

export interface ResponseEventResponse {
  event_id: string;
  accepted_sequence: number;
  current_revision: number;
  draft: ExamDraft;
}

export interface EventReceiptResponse {
  event_id: string;
  client_event_id: string;
  client_sequence: number;
  accepted_revision: number;
  instance_id: string;
  action: string;
  option_id?: string;
}

export interface TelemetryGapsRequest {
  expected_revision: number;
  reason: "lost_queue" | "recovered_without_queue" | "client_gap" | string;
}

export interface TelemetryGapsResponse {
  telemetry_incomplete: boolean;
  current_revision: number;
  last_ack_sequence: number;
}

export interface SubmitExamRequest {
  expected_revision: number;
  submission_id: string;
  last_ack_sequence: number;
}

export interface SubmitExamResponse {
  assessment_id: string;
  status: string;
  result_revision: number;
  result_url: string;
}

export interface AbandonExamRequest {
  expected_revision: number;
}

export interface AbandonExamResponse {
  status: string;
  result_url?: string;
}

export interface ResultOption {
  id: string;
  text: string;
}

export interface ExamResultQuestionItem {
  instance_id: string;
  ordinal: number;
  task_family: string;
  stem: string;
  options: ResultOption[];
  selected_option_id: string | null;
  correct_option_id: string;
  is_correct: boolean;
  explanation: string;
  first_correct: boolean | null;
  response_history: string;
}

export interface ExamResponseAnalysis {
  first_choice_correct_count: number;
  revised_to_correct_count: number;
  revised_to_incorrect_count: number;
  reliable_first_choices: number;
}

export interface ExamResultResponse {
  result_revision: number;
  total: number;
  correct: number;
  omitted: number;
  void: number;
  ratio: number | null;
  questions: ExamResultQuestionItem[];
  response_analysis: ExamResponseAnalysis;
}
