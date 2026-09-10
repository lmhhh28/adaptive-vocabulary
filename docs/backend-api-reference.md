# 后端接口完整参考（跨平台适配）

**Target audience**: Frontend developers on any platform (iOS/Android/Flutter/React Native/Web/Desktop)

本文档是自适应词汇应用后端接口的权威参考。无论您在哪个平台上开发客户端，都必须严格遵循本指南中的接口定义、数据结构、安全限制以及错误处理要求。

---

## 1. 概述

- **Base URL**: `http://127.0.0.1:8080`
- **API 基础路径**: `/api/v1`（注意 `/client-context` 和 `/` 不在此前缀下）
- **传输**: JSON over HTTP, 编码为 UTF-8
- **全局请求正文上限**: 2 MiB，超过此限制将返回 413 错误。
- **分页**: 默认 `limit` 为 50，最大值为 200。部分集合接口支持使用 `offset` 偏移量。
- **ID 格式**: 所有实体 ID 均为标准的 UUID v4 字符串。

---

## 2. 认证与安全

### 2.1 会话与 CSRF

系统通过 Session Cookie 和跨站请求伪造 (CSRF) 令牌机制来保护接口：
- `GET /client-context` 返回 `{ "csrf_token": "string" }` 并在响应头中设置 `avocab_session` Cookie（属性包括 HttpOnly, Path=/, SameSite=Lax, Secure=false for local HTTP）。
- 所有 `POST`, `PUT`, `DELETE`, `PATCH` 等会改变状态的请求必须包含：
  - **Cookie**: `avocab_session`（浏览器会自动管理，非浏览器需自行维护 Cookie Jar）。
  - **Header**: `X-CSRF-Token: <token from /client-context>`
- **CSRF token 格式**: `<unix_seconds>.<hmac_sha256_hex>`，有效期为 24 小时。
- **CSRF 拒绝处理**: 遇到 HTTP 403 且错误代码为 `CSRF_REJECTED` 时，客户端需静默重新调用 `/client-context` 获取新的 token，并保持挂起的操作以供重试。
- **非浏览器客户端特别说明**: 必须实现 Cookie Jar 机制并在请求中携带 Cookie，同时显式管理 CSRF token 生命周期。

### 2.2 Host/Origin 限制
- 仅接受本地环回地址 (Loopback) 请求：`127.0.0.1`, `localhost`, `::1`。
- 对于包含 `Origin` 请求头的写请求，`Origin` 必须是上述环回地址之一。
- **不提供 CORS 支持**；所有跨域请求均会被拒绝。

### 2.3 Worker 认证（内部接口）
- `/internal/v1/generation/*` 路径下的接口需要 Bearer Token：`Authorization: Bearer <worker.token>`。
- `worker.token` 在服务端首次启动时自动生成，并保存在数据目录的 `data-dir/worker.token` 中。
- **绝对不要**将 `worker.token` 暴露给前端客户端。

---

## 3. 通用约定

### 3.1 幂等性

- 所有写入或变更接口（Mutation Endpoints）**应该**（部分为必须）发送 `Idempotency-Key: <UUID>` 请求头。
- **键的作用域**：HTTP 方法 + 路由模板 + 资源 ID。
- **请求摘要 (Digest)**：针对 Canonical Compact JSON（键名排序，无重复键）计算的 SHA-256 哈希值。
- 同一键 + 相同摘要 = 重放成功（可能包含响应头 `Idempotent-Replayed: true`）。
- 同一键 + 不同摘要 = 冲突，返回 `409 IDEMPOTENCY_CONFLICT`。
- 只有成功的事务（如 200, 201）会保存幂等性记录。
- 部分接口使用请求正文中的字段作为隐式幂等键（例如考试响应事件中的 `client_event_id`，提交时的 `submission_id`）。

**接口幂等性支持度：**

| 接口类型 | 幂等性支持级别 | 隐式或显式 |
|---|---|---|
| 查询接口 (GET) | 不适用 | 无 |
| 资源创建 (POST) | 完全支持 | 显式需要 Idempotency-Key |
| 练习尝试 / 考试事件 | 完全支持 | 隐式使用 attempt_id / client_event_id |
| 更新/归档 (POST/PATCH) | 完全支持 | 显式需要 Idempotency-Key |

### 3.2 乐观并发控制
- `expected_revision`: 整数类型，资源创建时为 0，每次状态变更递增。
- `expected_cursor`: 练习会话中的题目游标。
- `client_sequence`: 考试响应事件序号。
- 如果请求体缺失必需的版本字段，将返回 `428 PRECONDITION_REQUIRED`。
- 如果版本字段与服务器当前状态不匹配，将返回 `409 REVISION_CONFLICT`，错误详情 `details` 中将提供 `current_revision` 供客户端参考。

### 3.3 时间格式
- 大多数时间戳表示为 **Unix 微秒 (Unix Microseconds)**（整数，如 `created_at`, `updated_at`, `deadline_at`, `due_at`）。
  - **转换为 Date**: `new Date(Math.trunc(value / 1000))`。
- 考试截止时间 `deadline_at` 在部分公开响应中可能表现为 RFC 3339 UTC 字符串（例如 `"2026-09-10T04:00:00Z"`）。
  - **转换为 Date**: 直接解析为 ISO 8601。
- **切勿**将微秒值直接传递给期望接收毫秒的 Date 构造函数。

### 3.4 空值处理
- `0` 是一个有效的 `revision`, `cursor`, `ordinal`, `sequence` 数值。请使用 `null` 检查，而非 Falsy（例如 `if (value === null)`，而不是 `if (!value)`）。
- 未设置的可选字段将在 JSON 响应中被省略。
- 某些显式的 null 值（例如 `selected_option_id`, `first_correct`, `ratio`）可能存在于响应中，需妥善处理。
- 空数组可能被序列化为 null，客户端应统一视作空数组处理（如 `items ?? []`）。
- 服务端部分零值字段可能因 Go 的 `omitempty` 标签而被省略。

---

## 4. 统一错误格式

所有非 2xx 响应均会遵循统一的错误数据格式：

```json
{
  "error": {
    "code": "ERROR_CODE",
    "message": "Human-readable message",
    "request_id": "uuid",
    "details": { /* context-specific fields */ }
  }
}
```

### 错误码参考表

| HTTP 状态 | Error Code | 触发条件 | 客户端处理建议 |
|---|---|---|---|
| 400 | INVALID_REQUEST | 输入无效、格式错误、导入超限 | 校验并修正用户输入 |
| 400 | BANK_ARCHIVED | 试图对已归档的词库执行操作 | 提示用户选择其他未归档题库 |
| 401 | UNAUTHORIZED | 未授权或凭证无效 | 重新建立会话（重获 Token） |
| 403 | ORIGIN_REJECTED | 请求头 Origin 非环回地址 | 确保同源或使用环回地址发起请求 |
| 403 | CSRF_REJECTED | CSRF token 无效或过期 | 重新获取 `/client-context` 后重试请求 |
| 404 | NOT_FOUND | 资源不存在 | 检查请求路径或资源 ID |
| 409 | IDEMPOTENCY_CONFLICT | 相同的幂等键但请求体不同 | 检查本地操作队列是否有冲突操作 |
| 409 | REVISION_CONFLICT | `expected_revision` 不匹配 | 刷新资源状态重新拉取当前版本号 |
| 409 | EVENT_SEQUENCE_CONFLICT | `client_sequence` 重复提交 | 忽略或检查考试事件队列状态 |
| 409 | EVENT_SEQUENCE_GAP | `client_sequence` 跳跃跳号 | 触发与服务端的事件队列同步 |
| 409 | SESSION_ACTIVE | 在有活跃会话时尝试修改相关题库 | 提示用户需先结束当前活跃会话 |
| 409 | SESSION_CLOSED | 会话已被结束、提交或废弃 | 刷新状态，引导重新进入或查看结果 |
| 409 | ALREADY_ANSWERED | 尝试对同一题目多次提交答案 | 使用已有反馈，推进至下一题 |
| 409 | EXAM_SUBMITTED | 考试已提交（可能触及 Deadline）| 导航至考试结果页面 |
| 409 | LEASE_LOST | Worker 租约到期 | 停止当前 Worker 操作 |
| 409 | INPUT_VERSION_CHANGED | Worker 输入状态版本已改变 | 重新认领任务 |
| 409 | BUDGET_EXHAUSTED | 生成资源额度超限 | 暂停发送生成请求 |
| 409 | CALL_ATTEMPTS_EXHAUSTED | 网络重试次数达到上限 | 停止重试并提示用户网络问题 |
| 409 | CALL_SETTLEMENT_CONFLICT| 结算摘要不匹配 | 放弃覆盖或提醒用户 |
| 413 | PAYLOAD_TOO_LARGE | 请求体超过 2 MiB | 减小 Payload，或分批处理 |
| 415 | UNSUPPORTED_MEDIA_TYPE | `Content-Type` 错误 | 修正为 `application/json` 或所需类型 |
| 422 | EXAM_CONSTRAINT_UNSATISFIED | 无法生成满足约束的试卷 | 减少试卷请求题数 |
| 422 | ASSEMBLY_LIMIT_REACHED | 搜寻候选预算用尽 | 调整配额或重试 |
| 428 | PRECONDITION_REQUIRED | 请求体缺少必要的并发版本或游标| 在请求体加入所需字段 |
| 503 | MAINTENANCE | 系统正处于维护或重建模式 | 暂停请求，等待维护完成 |
| 503 | DATABASE_BUSY | 数据库繁忙 (SQLite) | 稍后进行带避让的重试 |
| 503 | GENERATION_NOT_CONFIGURED | LLM 生成功能未配置 | 在设置中配置模型及 API 密钥 |

**`details` 对象字段白名单**：`current_revision`, `expected_next_sequence`, `last_ack_sequence`, `cursor`, `session_id`, `session_kind`, `counts`, `limits`, `retryable`, `acceptance` (通常为 `"not_accepted"`), `requires_rebuild`。

---

## 5. 数据类型定义

以下类型定义使用 TypeScript 语法，作为跨平台共享模型的通用参考。

```typescript
// === Enums ===
type TaskFamily = 'meaning_recognition' | 'reverse_selection' | 'context_selection';
type SessionKind = 'practice' | 'exam';
type SessionStatus = 'open' | 'ended' | 'abandoned' | 'submitted';
type ImportItemStatus = 'resolved' | 'ambiguous' | 'not_found' | 'invalid' | 'excluded';
type ImportAction = 'adopt' | 'exclude';
type DispositionAction = 'disputed' | 'invalidate' | 'restore_validity';
type ResponseAction = 'select' | 'clear' | 'noop';
type CandidateDisposition = 'published' | 'auto_passed' | 'rejected' | 'uncertain' | 'pending_maintenance';
type FindingResolutionAction = 'revise' | 'exclude' | 'regenerate' | 'restore_validity';
type GenerationJobStatus = 'pending' | 'processing' | 'completed' | 'failed' | 'needs_attention' | 'budget_exhausted';

// === Unix Microseconds ===
type UnixMicroseconds = number; // Integer, microseconds since epoch

// === Public DTOs (safe for end-user display) ===
type PublicOption = { id: string; text: string };
type PublicQuestion = {
  instance_id: string;
  ordinal: number;        // 0-based
  task_family: TaskFamily;
  stem: string;
  options: PublicOption[]; // Always 4 options, no answer info
};

// === Session ===
type Session = {
  id: string;
  kind: SessionKind;
  status: SessionStatus;
  bank_id: string | null;
  cursor: number;
  normal_position: number | null;
  revision: number;
  last_ack_sequence: number;
  created_at: UnixMicroseconds;
  updated_at: UnixMicroseconds;
};
type ActiveSession = Session & { deadline_at: UnixMicroseconds | null };

// === Practice ===
type AttemptFeedback = {
  attempt_id: string;
  correct: boolean;
  correct_option_id: string;
  explanation: string;
  next_cursor: number;
  session_revision: number;
};

// === Exam ===
type ExamDraft = {
  instance_id: string;
  selected_option_id: string | null;
  revision_count: number;
};
type ExamResultQuestion = PublicQuestion & {
  selected_option_id: string | null;
  correct_option_id: string;
  is_correct: boolean;
  explanation: string;
  first_correct: boolean | null; // null = no reliable first choice
  response_history: string;      // 'single_response' | 'revised' | 'omitted' | 'telemetry_incomplete'
};
type ResponseAnalysis = {
  first_choice_correct_count: number;
  revised_to_correct_count: number;
  revised_to_incorrect_count: number;
  reliable_first_choices: number;
};
type ExamResult = {
  result_revision: number;
  total: number;
  correct: number;
  omitted: number;
  void: number;
  ratio: number | null; // 0-1 scale, multiply by 100 for percentage
  questions: ExamResultQuestion[] | null;
  response_analysis: ResponseAnalysis;
};

// === Source ===
type Source = {
  id: string; name: string; location: string; source_version: string;
  license: string; usage_scope: string; checksum?: string;
  revision: number; created_at: UnixMicroseconds; updated_at: UnixMicroseconds;
};

// === Bank ===
type BankSummary = {
  id: string; name: string; note: string; archived: boolean; revision: number;
  member_count: number; task_counts: Partial<Record<TaskFamily, number>>;
  created_at: UnixMicroseconds; updated_at: UnixMicroseconds;
};
type BankMember = {
  sense_id: string; canonical_form: string; part_of_speech: string;
  meaning: string; source_id: string; added_at: UnixMicroseconds;
};
type BankDetail = BankSummary & { members: BankMember[] | null };

// === Import ===
type SenseCandidate = {
  sense_id: string; lexeme_id: string; part_of_speech: string;
  meaning: string; source_id: string;
};
type ImportItem = {
  id: string; import_id: string; bank_id?: string; ordinal: number;
  raw_input: string; normalized_word: string; specified_sense_id?: string;
  status: ImportItemStatus;
  candidates: SenseCandidate[] | null;
  resolved_sense_ids: string[] | null;
  disposition_reason?: string; revision: number;
  created_at: UnixMicroseconds; updated_at: UnixMicroseconds;
};

// === Generation ===
type BudgetUsage = {
  authorized_requests: number; max_requests: number;
  total_tokens: number; max_tokens: number; budget_overrun: boolean;
};

// === Health & Maintenance ===
type HealthResponse = {
  status: 'ok'; schema_version: number; app_version: string;
  contract_version: string; mode: 'active' | 'rebuilding';
};
type MaintenanceResponse = {
  mode: 'active' | 'rebuilding'; active_version: string;
  requires_rebuild: boolean;
  run_id?: string; run_status?: string; next_action?: string;
};
```

---

## 6. 接口详细参考

### 6.1 系统与上下文

#### 1. GET `/`
- **简述**: HTML 首页。非 API 接口。

#### 2. GET `/client-context`
- **简述**: 获取客户端上下文与 CSRF 令牌。
- **需要认证**: 否
- **请求头**: 无
- **请求体**: 空
- **成功响应 (200)**: `{ "csrf_token": "string" }`
- **副作用**: 如果未存在有效 Cookie，将设置 `avocab_session`。
- **备注**: 在任何会导致状态变更的操作前调用此接口。Token 24小时有效。服务器重启会使所有现有 token 失效。

#### 3. GET `/api/v1/health`
- **简述**: 获取应用健康状态。
- **需要认证**: 否
- **成功响应 (200)**: `HealthResponse`
- **备注**: 客户端在初始化或遭遇疑似宕机错误时调用。

#### 4. GET `/api/v1/maintenance`
- **简述**: 查询系统当前的维护状态。
- **需要认证**: 否
- **成功响应 (200)**: `MaintenanceResponse`
- **备注**: `next_action` 可能为 `'restart'` 或 `'resume_or_abort'`，主要用于 UI 状态提示。

#### 5. POST `/api/v1/test/action`
- **简述**: CSRF 机制测试专用接口。
- **注意**: 仅限开发环境，**切勿在生产环境客户端调用**。

### 6.2 活动会话

#### 6. GET `/api/v1/sessions/active`
- **简述**: 查询当前是否存在未完成的活跃练习或考试会话。
- **需要认证**: 是
- **成功响应 (200)**: `{ "session": ActiveSession | null }`
- **备注**: 客户端在启动或唤醒时必须检查，以恢复被中断的进度。

### 6.3 来源管理

#### 7. GET `/api/v1/sources`
- **简述**: 列出系统中所有已知的数据来源。
- **成功响应 (200)**: `{ "items": Source[] | null }`
- **备注**: 当前不支持分页查询。

#### 8. GET `/api/v1/sources/{id}`
- **简述**: 获取特定数据来源的详细信息。
- **成功响应 (200)**: `{ "source": Source }`
- **错误**: 404 (找不到资源)。

#### 9. POST `/api/v1/sources`
- **简述**: 创建新的数据来源。
- **请求头**: `Idempotency-Key` (必需)
- **请求体**: `{ "name": "string", "location": "string", "source_version": "string", "license": "string", "usage_scope": "string", "checksum": "string?" }`
- **限制**: 除 `checksum` 外所有字段均为必需且不能全为空白。
- **成功响应 (201)**: `{ "source_id": "string", "revision": number }`

#### 10. POST `/api/v1/sources/{id}/imports`
- **简述**: 导入外部条目至数据源。
- **请求头**: `Idempotency-Key` (必需)
- **请求体**:
  ```typescript
  {
    source_version: string,
    expected_revision: number,
    entries: { external_entry_id: string, raw_word: string, raw_translation: string, pos?: string, tags?: string, aliases?: string, statistics?: string }[]
  }
  ```
- **成功响应 (200)**: `{ "import_id": "string", "status": "completed" }`
- **错误**: `409 REVISION_CONFLICT`, `409 SOURCE_CONFLICT`。

### 6.4 题库管理

#### 11. GET `/api/v1/banks`
- **简述**: 列出题库摘要。
- **成功响应 (200)**: `{ "items": BankSummary[] | null }`

#### 12. POST `/api/v1/banks`
- **简述**: 创建新题库。
- **请求头**: `Idempotency-Key` (必需)
- **请求体**: `{ "name": "string", "note": "string?" }`
- **限制**: `name` 长度为去掉前后空格后的 1-120 个 UTF-8 字节。
- **成功响应 (201)**: `{ "bank_id": "string", "revision": number }`

#### 13. GET `/api/v1/banks/{id}`
- **简述**: 获取指定题库的内容与成员详情。
- **成功响应 (200)**: `{ "bank": BankDetail }`
- **错误**: `409 SESSION_ACTIVE` （防泄漏机制：如果此时用户正处于考试或练习会话中，则无法查看详情，以防透题）。

#### 14. POST `/api/v1/banks/{id}/members/remove`
- **简述**: 将词汇从题库中移除。
- **请求头**: `Idempotency-Key` (必需)
- **请求体**: `{ "sense_ids": string[], "expected_revision": number }`
- **限制**: `sense_ids` 数量应为 1-1000 项。
- **成功响应 (200)**: `{ "bank_id": "string", "revision": number, "removed_count": number }`
- **错误**: `409 SESSION_ACTIVE`, `409 REVISION_CONFLICT`, `503 MAINTENANCE`。

#### 15. POST `/api/v1/banks/{id}/archive`
- **简述**: 归档题库（软删除）。
- **请求头**: `Idempotency-Key` (必需)
- **请求体**: `{ "expected_revision": number }`
- **成功响应 (200)**: `{ "bank_id": "string", "archived": true, "revision": number }`

### 6.5 词表导入

#### 16. POST `/api/v1/banks/{id}/imports`
- **简述**: 导入词汇到题库。
- **支持三种模式**:
  1. **JSON 数组**: `{ "words": string[], "expected_revision": number, "resolution_policy_version": "v1" }`
  2. **JSON 文本**: `{ "text": string, "expected_revision": number, "resolution_policy_version": "v1" }`
  3. **Multipart/Form-Data**: 提供 `file` (文件流), `expected_revision`, `resolution_policy_version`。如果拓展名为 `.csv`，将被按 CSV 解析（列为 word, sense_id）；否则按每行一个单词读取。
- **限制**: 最大支持 5000 个非空条目，文本必须为有效 UTF-8。
- **成功响应 (200)**: `{ "import_id": "string", "status": "completed", "total_count": number, "resolved_count": number, "ambiguous_count": number, "not_found_count": number, "invalid_count": number, "excluded_count": number }`

#### 17. GET `/api/v1/imports/{id}/items`
- **简述**: 分页查询导入结果中的具体条目状态。
- **查询参数**: `status` (ambiguous|resolved|not_found|invalid|excluded) 可选, `limit` (1-200, 默认 50) 可选, `offset` (默认 0) 可选。
- **成功响应 (200)**: `{ "items": ImportItem[] | null, "total": number, "counts": Record<string, number> }`

#### 18. POST `/api/v1/import-items/{id}/resolve`
- **简述**: 手动介入解决歧义或冲突的导入项。
- **请求体**: `{ "action": "adopt" | "exclude", "sense_refs": string[], "reason": "string", "expected_revision": number }`
- **限制**: 当 `action` 为 `adopt` 时，`sense_refs` 必须提供对应采纳的义项字符串 ID。
- **成功响应 (200)**: `{ "item": ImportItem, "revision": number }`

### 6.6 练习

#### 19. POST `/api/v1/practice-sessions`
- **简述**: 开始一个新的练习会话。
- **请求头**: `Idempotency-Key` (必需)
- **请求体**: `{ "bank_id": "string", "question_count": number }`
- **成功响应 (201)**: `{ "session": ActiveSession }`

#### 20. GET `/api/v1/practice-sessions/{id}`
- **简述**: 获取指定练习会话的当前状态。
- **成功响应 (200)**: `{ "session": ActiveSession }`

#### 21. POST `/api/v1/practice-sessions/{id}/next`
- **简述**: 取得会话的下一道题目。
- **请求体**: `{ "expected_cursor": number }`
- **成功响应 (200)**: `{ "status": "active" | "completed", "question": PublicQuestion | null, "cursor": number }`
- **数据结构**: NextResponse 根据 `status` 的类型可能包含题目，如果是 `completed` 意味着练习结束，无题目返回。

#### 22. POST `/api/v1/practice-sessions/{id}/attempts`
- **简述**: 提交当前练习题目的答案。
- **请求头**: 不要求显示幂等键（隐式使用 `attempt_id`）。
- **请求体**:
  ```typescript
  {
    attempt_id: string, // UUID
    instance_id: string,
    expected_cursor: number,
    selected_option_id: string,
    telemetry: { time_to_answer_ms: number, interaction_count: number }
  }
  ```
- **成功响应 (200)**: `{ "feedback": AttemptFeedback }`

#### 23. POST `/api/v1/practice-sessions/{id}/end`
- **简述**: 正常结束已完成所有题目的练习会话。
- **请求体**: `{ "expected_cursor": number }`
- **成功响应 (200)**: `{ "session_id": string, "status": "ended" }`

#### 24. POST `/api/v1/practice-sessions/{id}/abandon`
- **简述**: 中止未完成的练习。
- **请求体**: `{ "expected_cursor": number }`
- **成功响应 (200)**: `{ "session_id": string, "status": "abandoned" }`

### 6.7 考试

#### 25. POST `/api/v1/exams`
- **简述**: 开启新的模拟考试。
- **请求头**: `Idempotency-Key` (必需)
- **请求体**: `{ "bank_id": "string", "question_count": number, "duration_minutes": number }`
- **成功响应 (201)**: `{ "session": ActiveSession, "questions": PublicQuestion[] }`

#### 26. GET `/api/v1/exams/{id}`
- **简述**: 恢复正在进行的考试草稿内容。
- **成功响应 (200)**: `{ "session": ActiveSession, "questions": PublicQuestion[], "drafts": ExamDraft[] }`

#### 27. POST `/api/v1/exams/{id}/response-events`
- **简述**: 向后端追加考试作答事件（支持防抖队列）。
- **请求体**:
  ```typescript
  {
    client_event_id: string, // 隐式幂等键
    client_sequence: number,
    client_timestamp: UnixMicroseconds,
    instance_id: string,
    action: "select" | "clear" | "noop",
    selected_option_id: string | null
  }
  ```
- **成功响应 (200)**: `{ "event_id": string, "accepted": boolean, "server_sequence": number, "last_ack_sequence": number }`
- **Rebase 流程**: 假如返回 `accepted: false` 并且附带 `409` 错误详情 `acceptance: "not_accepted"`，客户端需重新同步序列或检查 `last_ack_sequence` 以决定是否需要丢弃或重建队列。

#### 28. GET `/api/v1/exams/{id}/response-events/{client_event_id}`
- **简述**: 验证某事件的被接纳状态。
- **成功响应 (200)**: `{ "accepted": boolean, "server_sequence": number }` 或 404。

#### 29. POST `/api/v1/exams/{id}/telemetry-gaps`
- **简述**: 填补在断网下累积的遥测数据缺口。
- **请求体**: `{ "expected_revision": number, "gap_data": { instance_id: string, total_time_ms: number }[] }`
- **成功响应 (200)**: `{ "processed": true }`

#### 30. POST `/api/v1/exams/{id}/submit`
- **简述**: 提交完整试卷。
- **请求体**: `{ "submission_id": string, "last_sequence": number }`
- **成功响应 (200)**: `{ "session_id": string, "status": "submitted" }`

#### 31. POST `/api/v1/exams/{id}/abandon`
- **简述**: 提前放弃考试。
- **请求体**: `{ "last_sequence": number }`
- **成功响应 (200)**: `{ "session_id": string, "status": "abandoned" }`

#### 32. GET `/api/v1/exams/{id}/result`
- **简述**: 查阅提交后的考试得分与正确详情。
- **成功响应 (200)**: `{ "result": ExamResult }`

### 6.8 生成任务与质量
*(主要给管理员和数据准备阶段使用，但在部分 UI 需呈现。)*

#### 33. POST `/api/v1/banks/{id}/generation-jobs`
- **请求头**: `Idempotency-Key` (必需)
- **请求体**: `{ "expected_revision": number, "batch_size": number, "strategy": string }`
- **成功响应 (202)**: `{ "job_id": string, "status": GenerationJobStatus }`

#### 34. GET `/api/v1/generation-jobs/{id}`
- **成功响应 (200)**: `{ "job": GenerationJob, "budget_usage": BudgetUsage }`

#### 35. GET `/api/v1/question-candidates/{id}`
- **成功响应 (200)**: `{ "candidate": QuestionCandidate }`

#### 36. GET `/api/v1/quality-findings`
- **查询参数**: `status`, `limit`, `offset`
- **成功响应 (200)**: `{ "findings": QualityFinding[], "total": number }`

#### 37. POST `/api/v1/quality-findings/{id}/resolutions`
- **请求体**: `{ "action": FindingResolutionAction, "reason": string }`
- **成功响应 (200)**: `{ "finding_id": string, "status": "resolved" }`

### 6.9 证据处置与学习

#### 38. POST `/api/v1/question-versions/{id}/evidence-dispositions`
- **请求体**: `{ "action": DispositionAction, "reason": string }`
- **成功响应 (200)**: `{ "version_id": string, "disposition": string }`

#### 39. POST `/api/v1/question-versions/{id}/retire`
- **请求体**: `{ "reason": string }`
- **成功响应 (200)**: `{ "version_id": string, "retired": true }`

#### 40. GET `/api/v1/learning/summary`
- **成功响应 (200)**: 用户的全局学习分析视图概要。

#### 41. GET `/api/v1/learning/senses/{id}`
- **成功响应 (200)**: 返回该义项的历史表现趋势图表数据。

---

## 7. 跨平台客户端实现指南

### 7.1 初始化流程
跨平台客户端应该在应用启动时按以下序列发起请求以初始化状态：

```text
[Client]                            [Server]
   |--- 1. GET /api/v1/health -------->| (Check API availability)
   |<-- 200 OK (HealthResponse) -------|
   |                                   |
   |--- 2. GET /client-context ------->| (Get CSRF token)
   |<-- 200 OK (Sets avocab_session) --|
   |                                   |
   |--- 3. GET /api/v1/maintenance --->| (Check rebuild mode)
   |<-- 200 OK (MaintenanceResponse) --|
   |                                   |
   |--- 4. GET /api/v1/sessions/active | (Check for in-progress work)
   |<-- 200 OK (ActiveSession | null) -|
   |                                   |
   |--- 5. GET /api/v1/banks --------->| (Load home screen data)
   |<-- 200 OK (BankSummaries) --------|
```

### 7.2 非浏览器平台适配
- **Cookie Jar 管理**: iOS (URLSession), Android (OkHttp), Flutter (Dio) 等默认通常不持久化 Cookie。请自行添加 Cookie 拦截器/中间件以捕捉 `avocab_session` 并附加于后续请求中。
- **CSRF 处理**: 解析 `/client-context` 返回结果并将其保存。对于所有写入操作请求头附上 `X-CSRF-Token`。捕获 403 `CSRF_REJECTED` 响应作为拦截器级别重试信号，重新调用获取令牌然后重试原请求。
- **幂去机制**: 本地引入 UUID 生成库。对于带重试机制的网络层（例如遭遇超时），重试必须发送与首次完全相同的 `Idempotency-Key` 和相同的 Payload Body 以防重复创建。

### 7.3 练习流程实现
1. 检查是否存在未完成的 ActiveSession (`GET /api/v1/sessions/active`)。
2. 若无则 `POST /api/v1/practice-sessions` 创建，若有则进入该会话界面。
3. `POST /api/v1/practice-sessions/{id}/next` 获取当手游标题目。
4. 展示题目 `stem` 和选项给用户。不可让用户知晓正确答案。
5. 用户选择后调用 `POST /api/v1/practice-sessions/{id}/attempts`，传入 UUID 作为隐式重放防护。
6. 根据响应的 `AttemptFeedback` 呈现正确与否并提供 `explanation`。
7. 回到步骤 3 继续，直到 Next 接口返回 `status: completed`。
8. 调用 `POST /api/v1/practice-sessions/{id}/end` 闭合练习。

### 7.4 考试流程实现
1. 考前发起 `POST /api/v1/exams` 创建。
2. **重点**：创建后服务器立即下发完整 `PublicQuestion[]`，客户端需在内存中维护题目状态并一次性呈现或分页呈现。
3. **事件队列管理**：对于每次点击/选择，追加至一个本地 FIFO 队列（存储 `action`, `selected_option_id`, 并分配递增的 `client_sequence`）。
4. 队列逐个向后消费调用 `POST /api/v1/exams/{id}/response-events`。服务端将确认接收（`last_ack_sequence`）。
5. 如果出现网络故障，稍后重试相同的请求。如果因乱序或跳号返回了 409 `not_accepted`，则视情况发起同步，并将 `client_sequence` 回退 (Rebase) 到基于服务端的 `last_ack_sequence` 上。
6. **遥测补充**：长时间离线导致中间丢失遥测的事件可通过 `POST /api/v1/exams/{id}/telemetry-gaps` 进行上传补救。
7. 最后，无论用户手动交卷还是时限到达自动交卷，均需发起 `POST /api/v1/exams/{id}/submit`。
8. 使用 `GET /api/v1/exams/{id}/result` 渲染结果与得分详情。

### 7.5 离线与重连策略
- **不要擅自做主**: 任何写操作只要未收到服务器 2xx 确认，均不应在客户端永久保存为“已落盘状态”。对于练习，应当重试相同尝试直到被接纳，否则阻塞；对于考试，队列允许堆积（甚至离线答题），但在连接恢复时必须彻底清空队列才可请求交卷接口。
- **错误重试防抖**: 设计指数退避 (Exponential Backoff)，如重试达到系统配置最大次数遇到 `409 CALL_ATTEMPTS_EXHAUSTED`，告知用户停止并检查网络。

---

## 8. 接口索引表

| # | 方法 | 路径 | 成功状态码 | 需要幂等键 | 需要CSRF | 简述 |
|---|---|---|---|---|---|---|
| 1 | GET | `/` | 200 | 否 | 否 | HTML 首页 |
| 2 | GET | `/client-context` | 200 | 否 | 否 | 获取 CSRF token |
| 3 | GET | `/api/v1/health` | 200 | 否 | 否 | 探活健康检查 |
| 4 | GET | `/api/v1/maintenance` | 200 | 否 | 否 | 获取维护模式 |
| 5 | POST | `/api/v1/test/action` | - | 否 | 是 | CSRF 检查测试用 |
| 6 | GET | `/api/v1/sessions/active` | 200 | 否 | 否 | 检查活跃会话 |
| 7 | GET | `/api/v1/sources` | 200 | 否 | 否 | 获取源列表 |
| 8 | GET | `/api/v1/sources/{id}` | 200 | 否 | 否 | 查阅特定源 |
| 9 | POST | `/api/v1/sources` | 201 | 是 | 是 | 新建数据源 |
| 10 | POST | `/api/v1/sources/{id}/imports` | 200 | 是 | 是 | 源导入 |
| 11 | GET | `/api/v1/banks` | 200 | 否 | 否 | 列出题库 |
| 12 | POST | `/api/v1/banks` | 201 | 是 | 是 | 创建题库 |
| 13 | GET | `/api/v1/banks/{id}` | 200 | 否 | 否 | 题库详情与成员 |
| 14 | POST | `/api/v1/banks/{id}/members/remove` | 200 | 是 | 是 | 移除成员 |
| 15 | POST | `/api/v1/banks/{id}/archive` | 200 | 是 | 是 | 题库归档 |
| 16 | POST | `/api/v1/banks/{id}/imports` | 200 | 否 (文件模式下) | 是 | 执行导入操作 |
| 17 | GET | `/api/v1/imports/{id}/items` | 200 | 否 | 否 | 查看导入明细 |
| 18 | POST | `/api/v1/import-items/{id}/resolve` | 200 | 否 (部分包含预检) | 是 | 介入歧义选项 |
| 19 | POST | `/api/v1/practice-sessions` | 201 | 是 | 是 | 开设练习会话 |
| 20 | GET | `/api/v1/practice-sessions/{id}` | 200 | 否 | 否 | 查询练习状态 |
| 21 | POST | `/api/v1/practice-sessions/{id}/next` | 200 | 否 | 是 | 获取下题游标 |
| 22 | POST | `/api/v1/practice-sessions/{id}/attempts` | 200 | 隐式 | 是 | 提交单题答案 |
| 23 | POST | `/api/v1/practice-sessions/{id}/end` | 200 | 否 | 是 | 正常结束练习 |
| 24 | POST | `/api/v1/practice-sessions/{id}/abandon` | 200 | 否 | 是 | 放弃当前练习 |
| 25 | POST | `/api/v1/exams` | 201 | 是 | 是 | 开设考试会话 |
| 26 | GET | `/api/v1/exams/{id}` | 200 | 否 | 否 | 恢复考试缓存 |
| 27 | POST | `/api/v1/exams/{id}/response-events` | 200 | 隐式 | 是 | 追加队列事件 |
| 28 | GET | `/api/v1/exams/{id}/response-events/{id}`| 200 | 否 | 否 | 验证事件接纳状态 |
| 29 | POST | `/api/v1/exams/{id}/telemetry-gaps` | 200 | 否 | 是 | 提交遗漏遥测 |
| 30 | POST | `/api/v1/exams/{id}/submit` | 200 | 隐式 | 是 | 提交整套考卷 |
| 31 | POST | `/api/v1/exams/{id}/abandon` | 200 | 否 | 是 | 放下放弃考试 |
| 32 | GET | `/api/v1/exams/{id}/result` | 200 | 否 | 否 | 查询批改结果 |
| 33 | POST | `/api/v1/banks/{id}/generation-jobs` | 202 | 是 | 是 | 开启生成任务 |
| 34 | GET | `/api/v1/generation-jobs/{id}` | 200 | 否 | 否 | 查询任务状态 |
| 35 | GET | `/api/v1/question-candidates/{id}` | 200 | 否 | 否 | 查询题目生成候选 |
| 36 | GET | `/api/v1/quality-findings` | 200 | 否 | 否 | 聚合质量检查 |
| 37 | POST | `/api/v1/quality-findings/{id}/resolutions`| 200 | 否 | 是 | 处理质量反馈 |
| 38 | POST | `/api/v1/question-versions/{id}/evidence-dispositions` | 200 | 否 | 是 | 记录题意争议证据 |
| 39 | POST | `/api/v1/question-versions/{id}/retire` | 200 | 否 | 是 | 废除该问题版本 |
| 40 | GET | `/api/v1/learning/summary` | 200 | 否 | 否 | 学习进度概览 |
| 41 | GET | `/api/v1/learning/senses/{id}` | 200 | 否 | 否 | 个体义项表现追踪 |
