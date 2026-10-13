export interface BankSummary {
  id: string;
  name: string;
  note: string;
  archived: boolean;
  revision: number;
  member_count: number;
  task_counts: Record<string, number>;
  created_at: number;
  updated_at: number;
}

export interface BankMember {
  sense_id: string;
  canonical_form: string;
  part_of_speech: string;
  meaning: string;
  source_id: string;
  added_at: number;
}

export interface BankDetail extends BankSummary {
  members: BankMember[];
}

export interface Source {
  id: string;
  name: string;
  location: string;
  source_version: string;
  license: string;
  usage_scope: string;
  checksum?: string;
  revision: number;
  created_at: number;
  updated_at: number;
}

export interface SenseVersionItem {
  sense_id: string;
  sense_version_id: string;
  lexeme_id: string;
  canonical_form: string;
  definition: string;
  status: string;
}

export interface QuestionVersionItem {
  question_version_id: string;
  sense_id: string;
  form_id: string;
  task_family: string;
  status: string;
  revision: number;
}

export interface CreateBankRequest {
  name: string;
  note?: string;
}

export interface CreateSourceRequest {
  name: string;
  location: string;
  source_version: string;
  license: string;
  usage_scope: string;
  checksum?: string;
}

export interface RemoveMembersRequest {
  expected_revision: number;
  sense_ids: string[];
}

export interface ArchiveBankRequest {
  expected_revision: number;
}

export interface PaginatedResponse<T> {
  items: T[];
  next_cursor: string | null;
  has_more: boolean;
}
