export interface BudgetUsage {
  authorized_requests: number;
  max_requests: number;
  total_tokens: number;
  max_tokens: number;
  budget_overrun: boolean;
}

export interface JobCandidateItem {
  id: string;
  item_id: string;
  attempt_number: number;
  candidate_index: number;
  disposition: "published" | "auto_passed" | "rejected" | "uncertain" | "pending_maintenance" | string;
  published_version_id?: string;
  created_at: number;
}

export interface JobSummary {
  id: string;
  job_id?: string;
  bank_id: string;
  bank_name?: string;
  status: "pending" | "processing" | "completed" | "failed" | "needs_attention" | "budget_exhausted" | string;
  requested: number;
  requested_count?: number;
  published: number;
  published_count?: number;
  rejected: number;
  rejected_count?: number;
  pending: number;
  pending_count?: number;
  revision: number;
  budget_usage: BudgetUsage;
  fixed_input_digest: string;
  generation_config_version: string;
  quality_policy_version: string;
  created_at: number;
  updated_at: number;
  candidates?: JobCandidateItem[];
}

export interface SenseRef {
  sense_id: string;
  sense_version_id: string;
}

export interface CreateJobRequest {
  sense_refs: SenseRef[];
  generation_config_version: string;
  quality_policy_version: string;
  expected_revision: number;
}

export interface CreateJobResponse {
  job_id: string;
  status: string;
  fixed_input_digest: string;
}

export interface CandidateOption {
  text: string;
  is_correct: boolean;
  language?: string;
  sense_id?: string;
}

export interface CandidateContent {
  task_family?: string;
  stem?: string;
  options?: CandidateOption[];
  explanation?: string;
  metadata?: Record<string, any>;
}

export interface CandidateDetailResponse {
  candidate: CandidateContent;
  checks: Record<string, any>;
  provenance: Record<string, any>;
  disposition: string;
  revision: number;
}

export interface QualityFindingItem {
  id: string;
  job_id: string;
  candidate_id: string;
  finding_type: string;
  details: Record<string, any>;
  checker_version: string;
  status: "open" | "resolved" | "ignored" | string;
  resolution_id?: string;
  revision: number;
  created_at: number;
  updated_at: number;
}

export interface PaginatedResponse<T> {
  items: T[];
  next_cursor?: string | null;
  has_more: boolean;
}

export interface BankOption {
  id: string;
  name: string;
  revision: number;
}

export interface SenseVersionItem {
  sense_id: string;
  sense_version_id: string;
  lexeme_id: string;
  canonical_form: string;
  definition: string;
  status: string;
}
