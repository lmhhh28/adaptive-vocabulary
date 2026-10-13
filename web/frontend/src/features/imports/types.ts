export interface SenseCandidate {
  sense_id: string;
  lexeme_id: string;
  part_of_speech: string;
  meaning: string;
  source_id: string;
}

export type ImportItemStatus = "resolved" | "ambiguous" | "not_found" | "invalid" | "excluded";

export interface ImportItem {
  id: string;
  import_id: string;
  bank_id?: string;
  ordinal: number;
  raw_input: string;
  normalized_word: string;
  specified_sense_id?: string;
  status: ImportItemStatus;
  candidates: SenseCandidate[];
  resolved_sense_ids: string[];
  disposition_reason?: string;
  revision: number;
  created_at: number;
  updated_at: number;
}

export interface ImportJob {
  id: string;
  target_type: "source" | "bank";
  target_id: string;
  status: "pending" | "processing" | "completed" | "failed";
  source_version?: string;
  resolution_policy_version: string;
  raw_input_summary: string;
  total_count: number;
  resolved_count: number;
  ambiguous_count: number;
  not_found_count: number;
  invalid_count: number;
  excluded_count: number;
  revision: number;
  created_at: number;
  updated_at: number;
}

export interface ImportItemsResponse {
  items: ImportItem[];
  total: number;
  counts: Record<string, number>;
}

export interface ResolveItemRequest {
  action: "adopt" | "exclude";
  sense_refs?: string[];
  reason?: string;
  expected_revision: number;
}

export interface BankOption {
  id: string;
  name: string;
  note?: string;
  revision: number;
  member_count: number;
  archived: boolean;
}

export interface BankImportResponse {
  import_id: string;
  status: string;
  total_count: number;
  resolved_count: number;
  ambiguous_count: number;
  not_found_count: number;
  invalid_count: number;
  excluded_count: number;
}
