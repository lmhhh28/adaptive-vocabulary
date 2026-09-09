# 前端接口与字段参考

核对日期：2026-09-09。先阅读[联调指南](frontend-api.md)中的认证、错误和重试约定。下表使用完整路径；`{id}` 必须替换为对应资源返回的实际 ID。所有写操作均应发送 CSRF，并按指南保存幂等操作；“必填”指前端应完整发送的调用字段，当前校验缺口另外标明。

JSON 示例中的 UUID 用于说明结构；联调时使用服务实际返回的 bank/session/instance/option/sense ID，不将示例 ID 当作固定资源。

## 1. 接口索引

| 方法 | 路径 | 成功状态 | 用途 |
| --- | --- | --- | --- |
| GET | `/client-context` | 200 | 浏览器 Cookie / CSRF 上下文 |
| GET | `/api/v1/health` | 200 | 服务健康 |
| GET | `/api/v1/maintenance` | 200 | 维护状态 |
| GET | `/api/v1/sessions/active` | 200 | 当前活动会话 |
| GET | `/api/v1/sources` | 200 | 来源列表 |
| POST | `/api/v1/sources` | 201 | 登记来源 |
| GET | `/api/v1/sources/{id}` | 200 | 来源元数据 |
| POST | `/api/v1/sources/{id}/imports` | 200 | 同步导入来源条目 |
| GET | `/api/v1/banks` | 200 | 题库摘要列表 |
| POST | `/api/v1/banks` | 201 | 创建题库 |
| GET | `/api/v1/banks/{id}` | 200 | 题库成员详情 |
| POST | `/api/v1/banks/{id}/members/remove` | 200 | 移除成员 |
| POST | `/api/v1/banks/{id}/archive` | 200 | 归档题库 |
| POST | `/api/v1/banks/{id}/imports` | 200 | 同步导入词表 |
| GET | `/api/v1/imports/{id}/items` | 200 | 条目、分页、统计 |
| POST | `/api/v1/import-items/{id}/resolve` | 200 | 采纳/排除条目 |
| POST | `/api/v1/practice-sessions` | 201 / 200 | 创建/恢复练习 |
| GET | `/api/v1/practice-sessions/{id}` | 200 | 恢复练习详情 |
| POST | `/api/v1/practice-sessions/{id}/next` | 200 | 保留下一题或返回无候选 |
| POST | `/api/v1/practice-sessions/{id}/attempts` | 200 | 首次作答及反馈 |
| POST | `/api/v1/practice-sessions/{id}/end` | 200 | 结束练习 |
| POST | `/api/v1/practice-sessions/{id}/abandon` | 200 | 放弃练习，end 的默认 reason 别名 |
| POST | `/api/v1/exams` | 201 | 组卷 |
| GET | `/api/v1/exams/{id}` | 200 | 固定试卷和草稿 |
| POST | `/api/v1/exams/{id}/response-events` | 200 | 记录选择/清除 |
| GET | `/api/v1/exams/{id}/response-events/{client_event_id}` | 200 | 查询已接受事件回执 |
| POST | `/api/v1/exams/{id}/telemetry-gaps` | 200 | 遥测缺口屏障 |
| POST | `/api/v1/exams/{id}/submit` | 200 | 交卷或读取已有交卷结果 |
| POST | `/api/v1/exams/{id}/abandon` | 200 | 放弃；过期时自动交卷 |
| GET | `/api/v1/exams/{id}/result` | 200 | 评分与答案解析 |
| POST | `/api/v1/banks/{id}/generation-jobs` | 202 | 创建异步生成任务 |
| GET | `/api/v1/generation-jobs/{id}` | 200 | 任务与预算进度 |
| GET | `/api/v1/question-candidates/{id}` | 200 | 候选内容及检查结果 |
| GET | `/api/v1/quality-findings` | 200 | 质量异常列表 |
| POST | `/api/v1/quality-findings/{id}/resolutions` | 200 | 记录异常处置 |
| POST | `/api/v1/question-versions/{id}/evidence-dispositions` | 200 | 证据处置与重建 |
| POST | `/api/v1/question-versions/{id}/retire` | 200 | 停止后续使用该题目版本 |
| GET | `/api/v1/learning/summary` | 200 | 无答案的学习摘要 |
| GET | `/api/v1/learning/senses/{id}` | 200 | 词义的学习详情 |

无参数列表不隐含分页支持。题库详情、候选详情和词义学习详情会在存在活动会话时返回 `409 SESSION_ACTIVE`。方法不匹配应视为调用错误；不同 handler 的 405 响应格式并不完全统一。

## 2. 公共 DTO 和平台状态

以下 TypeScript 仅描述 HTTP 返回，不复用数据库模型：

```ts
type TaskFamily = 'meaning_recognition' | 'reverse_selection' | 'context_selection';
type UnixMicroseconds = number;
type PublicOption = { id: string; text: string };
type PublicQuestion = {
  instance_id: string;
  ordinal: number; // 从 0 开始
  task_family: TaskFamily;
  stem: string;
  options: PublicOption[]; // 四选一；不含答案、解析、正确性
};
type Session = {
  id: string;
  kind: 'practice' | 'exam';
  status: 'open' | 'ended' | 'abandoned' | 'submitted';
  bank_id: string | null;
  cursor: number;
  normal_position: number | null;
  revision: number;
  last_ack_sequence: number;
  created_at: UnixMicroseconds;
  updated_at: UnixMicroseconds;
};
type ActiveSession = Session & { deadline_at: UnixMicroseconds | null };
type ExamDraft = { instance_id: string; selected_option_id: string | null; revision_count: number };
type AttemptFeedback = {
  attempt_id: string; correct: boolean; correct_option_id: string; explanation: string;
  next_cursor: number; session_revision: number;
};
```

| 接口 | 响应 |
| --- | --- |
| GET /client-context | `{csrf_token:string}`，同时签发/复用 Cookie |
| GET /api/v1/health | `{status:"ok",schema_version:number,app_version:string,contract_version:string,mode:"active"或"rebuilding"}` |
| GET /api/v1/maintenance | `{mode,active_version:string,requires_rebuild:boolean,run_id?:string,run_status?:string,next_action?:string}` |
| GET /api/v1/sessions/active | `{session:ActiveSession或null}`；无活动会话时明确返回 null |

维护 `next_action` 当前可为 `restart` / `resume_or_abort`；它是 UI 提示，不是一个可直接 POST 的 URL。重建 run 状态可能为 `running`、`succeeded`、`failed`、`aborted`、`aborted_maintenance`，前端对未知状态保留原文并刷新。

## 3. 来源

来源对象 `Source`：`id,name,location,source_version,license,usage_scope` 为 string；`checksum?:string`；`revision,created_at,updated_at` 为整数。

### GET /api/v1/sources · GET /api/v1/sources/{id}

分别返回 `{items:Source[]或null}`、`{source:Source}`。列表按创建时间升序，不支持 cursor/limit。详情不存在返回 404。

### POST /api/v1/sources

要求 `Idempotency-Key`。请求示例：

```json
{
  "name": "自有教学词表",
  "location": "local:teaching-list",
  "source_version": "v1",
  "license": "自有内容",
  "usage_scope": "个人学习"
}
```

五个字段均为必填非空字符串，可另传 `checksum`。返回 `201 {source_id:string,revision:number}`。同键同正文可重放，但该路径不保证返回重放 Header；同键不同正文为 409。

### POST /api/v1/sources/{id}/imports

```json
{
  "source_version": "v1",
  "expected_revision": 0,
  "entries": [
    {"external_entry_id": "river-n-1", "raw_word": "river", "raw_translation": "河流", "pos": "n."}
  ]
}
```

`entries[]` 可含 `pos`、`tags`、`aliases`、`statistics`，这些附加字段当前 DTO 均为可选 **string**，不是数组或对象。必需提供可追溯的 external_entry_id、raw_word 和释义；当前实现会跳过某些空条目，前端应提交前校验。

返回 `200 {import_id:string,status:"completed"}`，当前同步完成并更新 source revision。下一次导入前重新 GET source。重复 external_entry_id 可能返回 `409 SOURCE_CONFLICT`；revision 不符为 `409 REVISION_CONFLICT`。这不是任意 URL 抓取接口。

依据：[bank handler](../internal/bank/handler.go)、[importer DTO](../internal/importer/types.go)、[importer handler](../internal/importer/handler.go)。

## 4. 题库

```ts
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
```

| 接口 | 请求 | 成功响应 / 特别说明 |
| --- | --- | --- |
| GET /api/v1/banks | 无 | `{items:BankSummary[]或null}`；返回所有题库，前端可按 archived 筛选 |
| POST /api/v1/banks | `{name:string,note?:string}` | `201 {bank_id,revision}`；要求 key；name 去首尾空白后非空，当前实现最大 **120 UTF-8 字节**，不等于 120 个中文字符 |
| GET /api/v1/banks/{id} | 无 | `{bank:BankDetail}`；活动会话时 409，列表摘要仍可读取 |
| POST /api/v1/banks/{id}/members/remove | `{sense_ids:string[],expected_revision:number}` | `{bank_id,revision,removed_count}`；sense_ids 为 1–1000 项，要求 key；仅移除题库成员关系，保留历史 |
| POST /api/v1/banks/{id}/archive | `{expected_revision:number}` | `{bank_id,archived:true,revision}`；要求 key，保留成员和历史 |

成员移除/归档缺少 expected_revision 返回 428，版本不符返回 409 并带 `details.current_revision`。活动会话或维护中拒绝修改。成员移除即使 removed_count=0 也可能递增 revision，以返回值为准。

`task_counts` 为对应可用内容计数，不应直接据此承诺满足考试去重、跨题干扰等组卷约束。

依据：[bank DTO / service](../internal/bank/bank.go)、[bank handler](../internal/bank/handler.go)。

## 5. 词表导入与消歧

### POST /api/v1/banks/{id}/imports

JSON 示例，words/text 选择一种发送：

```json
{
  "words": ["river", "bright"],
  "expected_revision": 0,
  "resolution_policy_version": "v1"
}
```

也可发送 `{text:"river\nbright",expected_revision:0,resolution_policy_version:"v1"}`。非空 words 优先；text 按 TXT 解析，即使其中包含逗号也不会自动变成 CSV。

文件上传示例（浏览器，不手工设置 multipart Content-Type）：

```js
const form = new FormData();
form.append('file', fileInput.files[0]);
form.append('expected_revision', String(bank.revision));
form.append('resolution_policy_version', 'v1');
const operationKey = crypto.randomUUID(); // 此用户动作只生成一次
const response = await fetch(`/api/v1/banks/${bank.id}/imports`, {
  method: 'POST', credentials: 'same-origin', cache: 'no-store',
  headers: { 'X-CSRF-Token': csrfToken, 'Idempotency-Key': operationKey },
  body: form
});
```

multipart 字段：`file` 必填，`expected_revision` 和 `resolution_policy_version` 显式发送。文件名 `.csv` 触发 CSV 解析；第一列为 word、第二列可选 sense ID。首行 `word` / `lexeme` / `vocabulary` 被识别为表头；空行跳过。词表为有效 UTF-8，最多 5000 个非空条目；超过长度或无效的单词可能记录为 invalid。重试保持文件内容、文件名和其他表单字段相同；它们参与摘要。

成功 `200`：

```json
{
  "import_id": "b9e84db4-03c2-416b-83c2-5ae88c61c57d",
  "status": "completed",
  "total_count": 2,
  "resolved_count": 1,
  "ambiguous_count": 1,
  "not_found_count": 0,
  "invalid_count": 0,
  "excluded_count": 0
}
```

重新 GET bank 获取 revision。当前是同步处理；`source_refs:string[]` 虽被解析但没有用于限制匹配来源。文件/条目超限、非 UTF-8、CSV 格式问题通常为 `400 INVALID_REQUEST`；归档题库为 `400 BANK_ARCHIVED`，版本冲突为 409。

### GET /api/v1/imports/{id}/items

参数：`status?`，`limit?` 默认 50、最大 200，`offset?` 默认 0。使用 `?status=ambiguous&limit=50&offset=0`，下一页增加 offset；不存在的 import 返回 404。

```ts
type SenseCandidate = {
  sense_id: string; lexeme_id: string; part_of_speech: string; meaning: string; source_id: string;
};
type ImportItem = {
  id: string; import_id: string; bank_id?: string; ordinal: number;
  raw_input: string; normalized_word: string; specified_sense_id?: string;
  status: 'resolved' | 'ambiguous' | 'not_found' | 'invalid' | 'excluded';
  candidates: SenseCandidate[] | null; resolved_sense_ids: string[] | null;
  disposition_reason?: string; revision: number;
  created_at: UnixMicroseconds; updated_at: UnixMicroseconds;
};
type ImportItemsResponse = {
  items: ImportItem[] | null; total: number; counts: Record<string, number>;
};
```

`total` / `counts` 是导入任务的汇总，不能用 items.length 代替。筛选时以返回 items 和分页 offset 控制加载。

### POST /api/v1/import-items/{id}/resolve

```json
{
  "action": "adopt",
  "sense_refs": ["00000000-0000-4000-8000-000000000201"],
  "reason": "人工确认词义",
  "expected_revision": 0
}
```

`sense_refs` 是候选中的 sense_id 字符串数组，不是 SenseRef 对象。adopt 至少一个；exclude 可省略或传空数组。返回 `{item:ImportItem,revision:number}`；重新查询条目和题库。非法 action/缺少采纳词义为 400，资源不存在为 404，版本或幂等冲突为 409。

依据：[parser](../internal/importer/parser.go)、[DTO](../internal/importer/types.go)、[handler](../internal/importer/handler.go)。

## 6. 练习

### POST /api/v1/practice-sessions · GET /api/v1/practice-sessions/{id}

创建请求 `{bank_id:string}`。返回 `{session:Session}`；新建为 201，同题库活动练习恢复或幂等重放可能为 200。其他题库/考试占有活动槽时为 409 SESSION_ACTIVE。

GET 详情返回 `{session:Session,current_question?:PublicQuestion,last_feedback?:AttemptFeedback}`。无相应内容时字段省略，三个练习会话响应（创建/详情/结束）均不含活动查询专有的 deadline_at。

### POST /api/v1/practice-sessions/{id}/next

请求 `{expected_cursor:number}`；缺少或 null 返回 428。成功是以下联合类型：

```ts
type NextResponse =
  | { status: 'ready'; session_revision?: number; cursor?: number; question: PublicQuestion }
  | { status: 'unavailable'; reason: string; counts?: Record<string, number> };
```

初始为 0 的 session_revision/cursor 会因 `omitempty` 被省略。`counts` 常见键为 cooldown、content_missing、exhausted，界面兼容未知计数。游标冲突为 `409 REVISION_CONFLICT` 并带 `details.cursor`；关闭会话为 409 SESSION_CLOSED。

### POST /api/v1/practice-sessions/{id}/attempts

请求字段：

| 字段 | 类型 | 用法 |
| --- | --- | --- |
| instance_id | string | 当前公开题目实例 ID |
| option_id | string | 当前四个选项中的一个 |
| submission_id | string | 前端生成并与 Idempotency-Key 保持相同；当前 handler 未强制，但前端不省略 |
| telemetry | object，可选 | 缺失时不编造展示/停留时间 |
| telemetry.client_active_duration_ms | integer，可选 | 有效停留时长，毫秒；不是 active_duration_ms |
| telemetry.displayed_at_client | integer，可选 | 客户端展示时间；当前 DTO 未统一声明单位，尚未与调用方约定时省略 |
| telemetry.interruption_flags | string[]，可选 | 中断标记 |
| telemetry.display_event_id | string，可选 | 展示事件标识 |

返回 `AttemptFeedback`。同实例同选项可返回原反馈；不同选项为 409 ALREADY_ANSWERED。实例/会话不存在为 404；无效选项为 400。实例不属于该会话的部分错误当前可能映射为 500，前端必须只使用该会话返回的实例。

### POST /api/v1/practice-sessions/{id}/end · /abandon

请求 `{expected_revision:number,reason?:"ended"|"abandoned"}`。`/end` 默认 ended，`/abandon` 默认 abandoned；reason 明确传入时仍以允许枚举为准。推荐显式发送 reason 和 key。

返回 `{session:Session,unresolved_unit_count:number}`，revision 递增。缺少 revision 为 428、非法 reason 为 400、旧版本为 409 REVISION_CONFLICT。相同已成功操作的 key/body 可在重启后重放；不同 key 对关闭会话的新操作可能返回 409 SESSION_CLOSED。

依据：[practice handler](../internal/practice/handler.go)、[DTO](../internal/practice/types.go)、[service](../internal/practice/service.go)。

## 7. 考试

### POST /api/v1/exams

```json
{
  "bank_id": "00000000-0000-4000-8000-000000000801",
  "question_count": 3,
  "task_quotas": {"meaning_recognition": 1, "reverse_selection": 1, "context_selection": 1},
  "time_limit_seconds": 1800
}
```

bank_id、正整数 question_count 必填。task_quotas 可省略；省略或空对象时按三题型均分，余数依次给词义识别、反向选择、语境选择。非空配额只允许三个题型键，值为非负整数，总和等于 question_count；省略的键视为 0。time_limit_seconds 可省略，有值时范围 60–14400。tags 可选 string[]。可组题数量由内容与约束决定，不据题库总数直接保证。

返回 `201 {exam_id,session_revision,deadline?,public_questions:PublicQuestion[],drafts:ExamDraft[],last_ack_sequence}`。deadline 是 RFC 3339；初始 revision/ack 为 0。使用返回 ID 跳转 `/exams/{id}`。

### GET /api/v1/exams/{id}

返回 `{id,status,session_revision,deadline?,public_questions,drafts,last_ack_sequence,result_url?}`。status 为 open/submitted/abandoned；result_url 仅已提交时出现。响应仍是公开试卷，不包含正确答案。

### POST /api/v1/exams/{id}/response-events

```json
{
  "instance_id": "ad9822f4-5f34-4d5b-bc21-2cdd7382a3b1",
  "action": "select",
  "option_id": "8a74b970-3f09-43f8-9477-8b63e813fd34",
  "client_event_id": "36352c99-bf36-4dc8-8aab-88829c287bfd",
  "client_sequence": 1,
  "expected_revision": 0
}
```

以上字段均显式发送；clear 时省略 option_id。Header key 与 client_event_id 相同。重新编排时可增加 `rebased_from:{client_sequence:number,expected_revision:number}`，其值记录原先被明确拒绝的版本。

返回 `{event_id,accepted_sequence,current_revision,draft:ExamDraft}`，不评分。重复选择同一选项或重复 clear 可能记录为 noop；noop 仍递增事件水位和 session revision，但不算一次改答。current_revision 与 accepted_sequence 都必须保存。

旧 revision/序号间隙/序号冲突返回 409 与明确的 `acceptance=not_accepted`；摘要冲突不能按未接受处理。过期的新动作返回 EXAM_SUBMITTED 并持久化交卷；已成功的历史事件可先命中回执。完整恢复算法见[联调指南](frontend-api.md#考试事件与交卷)。

### GET /api/v1/exams/{id}/response-events/{client_event_id}

返回 `{event_id,client_event_id,client_sequence,accepted_revision,instance_id,action,option_id?}`。action 可为 select/clear/noop；option_id 无值时省略。不存在为 404，当前查询错误也可能被映射为 404，不能将其当作可重排的权威拒绝。

### POST /api/v1/exams/{id}/telemetry-gaps

请求 `{expected_revision:number,reason:"lost_queue"|"recovered_without_queue"|"client_gap"}`，带 key。返回 `{telemetry_incomplete:true,current_revision:number,last_ack_sequence:number}`；只提高 revision，不提高已确认事件序号。后端当前未严格校验 reason 或 revision 字段存在性，前端仍按此枚举完整发送。

### POST /api/v1/exams/{id}/submit

请求 `{expected_revision:number,submission_id:string,last_ack_sequence:number}`；Header key 等于 submission_id。未过期时先等待待发送队列清空，再发送服务端已确认的水位。

返回 `{assessment_id,status:"submitted",result_revision:number,result_url:string}`。同 key 同正文重放原回执，同 key 改正文返回 409。已提交考试也可返回既有结果。未过期时 revision 冲突为 409，客户端 ack 落后于服务端为 428；deadline 到达时按服务端草稿自动结算。

### POST /api/v1/exams/{id}/abandon

请求 `{expected_revision:number}`。普通成功 `{status:"abandoned"}`；过期成功 `{status:"submitted",result_url:string}`。尚无持久化幂等回执；请求未知时 GET exam 确认，不直接换 key 再写。旧版本为 409，已关闭为 SESSION_CLOSED。此接口对缺失/非法 JSON 的校验不完整，前端必须发送正确正文。

### GET /api/v1/exams/{id}/result

仅已提交可读；未提交为 409 SESSION_ACTIVE，不存在为 404。

```ts
type ExamResultQuestion = PublicQuestion & {
  selected_option_id: string | null; correct_option_id: string;
  is_correct: boolean; explanation: string; first_correct: boolean | null;
  response_history: string;
};
type ExamResult = {
  result_revision: number; total: number; correct: number; omitted: number; void: number;
  ratio: number | null; questions: ExamResultQuestion[] | null;
  response_analysis: {
    first_choice_correct_count: number; revised_to_correct_count: number;
    revised_to_incorrect_count: number; reliable_first_choices: number;
  };
};
```

ratio 是 0–1 比例，乘 100 才是百分比；null 应显示“暂无有效分母”，不要与 0 分混淆。first_correct=null 表示没有可靠首次选择，不能渲染为答错。response_history 常见值 single_response、revised、omitted、telemetry_incomplete，保留兼容未知值的展示。

当前 `telemetry_incomplete` 与 first_correct 独立返回：已记录的首次选择仍可能有非 null 的 first_correct。不要仅凭 first_correct 非空认定完整遥测或 FSRS 更新资格。

报告可能因证据处置增加 result_revision 和 void。当前题目结果 DTO 没有逐题 is_void 标志，不应自行根据 is_correct 猜测哪题作废；统计使用报告的 correct/omitted/void/ratio。

依据：[exam DTO](../internal/exam/types.go)、[handler](../internal/exam/handler.go)、[assembler](../internal/exam/assembler.go)、[service](../internal/exam/service.go)。

## 8. 生成任务与质量异常

### POST /api/v1/banks/{id}/generation-jobs

请求：`{sense_refs:[{sense_id:string,sense_version_id:string}],generation_config_version:string,quality_policy_version:string,expected_revision:number}`。要求 key；sense_refs 非空且受服务策略上限限制，成员必须属于题库，版本必须存在。版本字符串取已配置版本，不随意填写。

返回 `202 {job_id,status:"pending",fixed_input_digest}`。202 只代表任务已创建；后续由 worker 执行，前端轮询 GET job。当前公开 bank member 不提供 sense_version_id，独立前端需要上游提供版本引用或另行补齐后端发现接口，不能从 sense_id 推断。

### GET /api/v1/generation-jobs/{id}

返回 `id,bank_id,status,requested,published,rejected,pending,revision,fixed_input_digest,generation_config_version,quality_policy_version,created_at,updated_at`，以及：

```ts
type BudgetUsage = {
  authorized_requests: number; max_requests: number; total_tokens: number;
  max_tokens: number; budget_overrun: boolean;
};
```

预算位于 `budget_usage`。状态可为 pending/running/completed/needs_attention/budget_exhausted/failed；对未知状态保留展示。不把“任务 completed”等同于“所有请求题目都 published”，同时显示计数与预算。该响应没有货币花费字段。

### GET /api/v1/question-candidates/{id}

返回 `{candidate:CandidateContent,checks:CandidateChecks,provenance:object,disposition:string,revision:number}`。活动会话时拒绝；当前 provenance 至少包含 candidate_id，不应假定已经内嵌全部供应商调用详情。

CandidateContent 包含 task_family、stem、options、explanation?；候选 option 可含 text、is_correct、language、sense_id 等管理字段，**不能作为 PublicOption 直接传入答题页面**。checks 包含 result、semantic_evaluations 和可选 rule_checks；逐项语义评级为 pass/reject/uncertain。

disposition 常见 published / auto_passed / rejected / uncertain / pending_maintenance。只有 published 才表明已产生正式版本；维护中保留的候选不等于已发布。

### GET /api/v1/quality-findings

支持 `job_id?`、`candidate_id?`、`status?`、`limit?`；limit 默认 50，有效范围 1–200，越界当前回退 50。返回 `{items:QualityFinding[]或null,next_cursor?:string}`，**当前没有生成 next_cursor 或处理后续 cursor**。

QualityFinding 字段：id、job_id、candidate_id、finding_type、details(object)、checker_version、status、resolution_id?、revision、created_at、updated_at。未解决常用 status=open，已处置 status=resolved。

### POST /api/v1/quality-findings/{id}/resolutions

请求 `{action,reason,content?,expected_revision}`；前端 action 使用 revise/exclude/regenerate/restore_validity，revise 提供 CandidateContent。返回 `{resolution_id,status:"resolved",revision:number}`。

当前实现只保存处置事实和更新 finding，不自动执行动作名称所暗示的内容改写、重新生成或恢复有效。尚无幂等回执；不要把这个接口当成发布接口。revision 冲突和部分非法请求当前可能为 500 INTERNAL_ERROR，资源不存在为 404。

依据：[generation public handler](../internal/generation/handler.go)、[DTO](../internal/generation/types.go)、[service](../internal/generation/service.go)。

## 9. 证据处置、停用和学习统计

### POST /api/v1/question-versions/{id}/evidence-dispositions

请求 `{action:"disputed"|"invalidate"|"restore_validity",reason:string,expected_revision:number}`，带 key。reason 必填；expected_revision 当前后端允许省略，前端必须提供版本。

返回 `{disposition_id,rebuild_run_id,status}`。正常同步完成 status=succeeded；失败可返回 status=failed，重放或其他并发观察也可能读到重建的持久化中间状态。HTTP 200 不代表重建一定成功。重试沿用同 key，不再创建重建；需要恢复时使用维护流程。

维护中 503；活动会话 409 SESSION_ACTIVE；旧版本 409 REVISION_CONFLICT；非法 action/reason 400；不存在 404。同 key 不同正文为 IDEMPOTENCY_CONFLICT。

### POST /api/v1/question-versions/{id}/retire

请求 `{reason:string,expected_revision:number}`；返回 `{version_id,status:"retired",revision:number}`。停用影响后续选题，历史事实保留；对学习证据的处置使用上一接口。当前没有幂等回执且没有统一的活动会话业务检查，管理 UI 应在无活动会话时进行；全局维护闸门仍适用。

### GET /api/v1/learning/summary

```json
{
  "counts_by_task": {},
  "evidence_conditions": {"ordinary_evidence_policy": "observation-v2", "active_version": "v1", "mode": "active"},
  "projection_version": "v1"
}
```

counts_by_task 按题型统计当前投影中的学习单元行数，不是题库题数、作答次数或掌握率；没有行的题型键可能省略。旧投影仍在维护期被读取，界面依据 mode / maintenance 显示待重建状态。

### GET /api/v1/learning/senses/{id}

返回 `{sense_id,projection_version,tasks:[{task_family,evidence,memory,confusions}]}`。tasks 覆盖三个题型；无相应投影时 evidence/memory 为对象但可能没有字段，confusions 可为空或 null。活动会话时返回 409。

| 对象 | 当前字段 |
| --- | --- |
| evidence | ordinary_samples/correct、normal_samples/correct、early_review_samples/correct、exam_single_samples/correct、correction_samples/correct、exam_revised_samples、exam_telemetry_incomplete_samples、omitted_samples（均为数值）；needs_consolidation（boolean）；last_ordinary_observed_at?（微秒）、last_ordinary_correct?（当前为 0/1 数值） |
| memory | fsrs_state、fsrs_step、stability、difficulty、due_at、reps、lapses；可选 last_review_at。时间为微秒，FSRS 参数不应直接显示为已校准掌握概率 |
| confusions[] | distractor_identity_id、stat_group、eligible_offered_count、wrong_selected_count |

当前不存在的 sense 不保证稳定返回 404，前端只使用已知 sense ID，不把空投影当成该词义必然存在的证据。

依据：[revision handler](../internal/revision/handler.go)、[DTO](../internal/revision/types.go)、[service](../internal/revision/service.go)、[maintenance handler](../internal/maintenance/handler.go)。
