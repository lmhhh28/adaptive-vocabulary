export interface QualityFindingItem {
  id: string;
  job_id: string;
  candidate_id: string;
  finding_type: string;
  details?: any;
  details_json?: string;
  checker_version: string;
  status: string; // 'open' | 'resolved'
  resolution_id?: string | null;
  revision: number;
  created_at: number;
  updated_at: number;
}

export interface PaginatedResponse<T> {
  items: T[];
  next_cursor?: string | null;
  has_more: boolean;
}

export interface ResolveFindingRequest {
  action: string;
  reason: string;
  expected_revision: number;
}

export interface ResolveFindingResponse {
  resolution_id: string;
  finding_id: string;
  status: string;
  revision: number;
}

export interface QuestionVersionItem {
  question_version_id: string;
  sense_id: string;
  form_id: string;
  task_family: string;
  status: string; // 'published' | 'retired' | 'invalidated' | 'disputed'
  revision: number;
}

export interface RetireRequest {
  reason: string;
  expected_revision: number;
}

export interface RetireResponse {
  version_id: string;
  status: string;
  revision: number;
}

export interface EvidenceDispositionRequest {
  action: "disputed" | "invalidate" | "restore_validity";
  reason: string;
  expected_revision: number;
}

export interface EvidenceDispositionResponse {
  disposition_id: string;
  rebuild_run_id: string;
  status: string;
}

export interface LearningSummaryResponse {
  counts_by_task: Record<string, number>;
  evidence_conditions: {
    ordinary_evidence_policy: string;
    active_version: string;
    mode: string;
  };
  projection_version: string;
}

export interface TaskLearningDetail {
  task_family: string;
  evidence: {
    ordinary_samples?: number;
    ordinary_correct?: number;
    normal_samples?: number;
    normal_correct?: number;
    early_review_samples?: number;
    early_review_correct?: number;
    exam_single_samples?: number;
    exam_single_correct?: number;
    correction_samples?: number;
    correction_correct?: number;
    needs_consolidation?: boolean;
    last_ordinary_observed_at?: number;
    last_ordinary_correct?: number;
  };
  memory: {
    fsrs_state?: string;
    fsrs_step?: number;
    stability?: number;
    difficulty?: number;
    due_at?: number;
    reps?: number;
    lapses?: number;
    last_review_at?: number;
  };
  confusions?: Array<{
    distractor_identity_id: string;
    stat_group: string;
    eligible_offered_count: number;
    wrong_selected_count: number;
  }>;
}

export interface SenseLearningDetail {
  sense_id: string;
  projection_version: string;
  tasks: TaskLearningDetail[];
}

export interface MaintenanceStatus {
  mode: string;
  run_id?: string | null;
  run_status?: string | null;
  active_version?: string;
  requires_rebuild: boolean;
  next_action?: string | null;
}
