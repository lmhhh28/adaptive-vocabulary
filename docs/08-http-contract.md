# HTTP v1 冻结契约

状态：1.1.0，2026-09-08，待业务实现。本文固定传输、并发和主流程字段；SQL/处理器尚未实现。六项修订的状态及资格定义见[10](10-review-resolutions.md)，模块启动/验收见[09](09-implementation-plan.md)。所有 ID 为 UUID 字符串，日期 RFC 3339 UTC；未知字段、非有限数值、类型错误拒绝。JSON 编码 UTF-8。

## 1. 通用约定

- 基础路径 `/api/v1`；JSON 请求/响应；HTTP 最大正文 2 MiB，词表 UTF-8 TXT/CSV 导入上限 5 MiB、10000 行，每行/字段上限 1000 字符。大文件分批；不接受任意远程 URL 抓取。分页 `limit` 默认 50、最大 200，opaque `cursor`。
- 所有状态写入需 `Idempotency-Key: <UUID>`，正文 `submission_id/client_event_id` 若存在必须等于 header。浏览器写操作同时校验 Origin 与 `X-CSRF-Token`；内部 worker 使用专用 bearer token。
- 幂等作用域为 HTTP method + 路由模板 + 资源 ID（创建会话的作用域为单人全局），请求摘要为解析/验证后按字段名排序、紧凑编码的 UTF-8 JSON 的 SHA-256；不允许重复 JSON key，数字仅使用 schema 指定整数，字符串不擅自 trim。键和摘要永久保留；大型响应以不可变对象引用重建。仅成功事务保存幂等结果，业务拒绝不占用 key。
- 处理顺序：认证/传输校验 → 查幂等/既有业务结果 → 写事务内读projection_control → 检查维护闸门 → 检查会话状态/截止时间 → 检查资源revision/游标 → 原子写入。成功重试即使原revision过期仍返回原响应并加Idempotent-Replayed:true；同键不同摘要409，换键不能越过业务唯一性。维护先于会话冲突返回MAINTENANCE；仅00/10明确允许的生成候选/检查点及预算账本写入豁免，正式发布仍暂停。维护进入和证据处置必须在同一事务检查无活动会话、写闸门/处置并冻结水位，不得部分生效。只读查询固定原活动投影，并返回mode、stale、requires_rebuild；不得把待重建旧状态显示为已应用修订。
- 有修改既有资源的操作均要求 `expected_revision`（next 使用 expected_cursor；训练提交由唯一 instance 和 cursor 约束）。缺失返回 428。revision 为从 0 开始的整数，成功的实质状态变更递增；noop 事件仍追加事实并递增 revision，但不算改答。
- 创建成功 201；查询/提交/同步变更 200；异步导入/生成任务 202。无候选的 next 返回 200 的 discriminated union，含 `status=unavailable`；不是 500，也不自动结束会话。
- 活动作答时内容/词义明细读取及编辑返回 409 SESSION_ACTIVE；详情范围见 00。所有答题、反馈、结果和包含内容的管理响应 `Cache-Control: no-store`。

统一错误体：

```json
{"error":{"code":"REVISION_CONFLICT","message":"状态已更新，请恢复当前会话","request_id":"uuid","details":{"current_revision":7,"last_ack_sequence":4}}}
```

`details`白名单：current_revision、expected_next_sequence、last_ack_sequence、cursor、session_id、session_kind、counts、limits、retryable、acceptance（仅not_accepted）、requires_rebuild。仅在10 R01要求的事务确认后返回acceptance，不给网络错误补造该字段；不回显SQL、token、选项正确性、答案或内部模型信息。

| HTTP | code | 客户端处理 |
| --- | --- | --- |
| 400/413/415 | INVALID_REQUEST / PAYLOAD_TOO_LARGE / UNSUPPORTED_MEDIA_TYPE | 修正输入 |
| 401/403 | UNAUTHORIZED / ORIGIN_REJECTED / CSRF_REJECTED | 重新建立本机会话或检查 worker token |
| 404 | NOT_FOUND | 资源不存在 |
| 409 | IDEMPOTENCY_CONFLICT / REVISION_CONFLICT / EVENT_SEQUENCE_CONFLICT / EVENT_SEQUENCE_GAP | 接受状态未知按原正文重试；明确not_accepted可按10 R01重排，不能把其他事件当本次回执 |
| 409 | SESSION_ACTIVE / SESSION_CLOSED / ALREADY_ANSWERED / EXAM_SUBMITTED | 恢复活动会话或查看既有结果 |
| 409 | LEASE_LOST / INPUT_VERSION_CHANGED | worker 停止本租约副作用 |
| 409 | BUDGET_EXHAUSTED / CALL_ATTEMPTS_EXHAUSTED | 不再发送供应商请求；保留已完成内容与预算账本 |
| 409 | CALL_SETTLEMENT_CONFLICT | 最终结算摘要不同，不覆盖已有usage或重复扣账 |
| 422 | EXAM_CONSTRAINT_UNSATISFIED / ASSEMBLY_LIMIT_REACHED | 显示可用范围/搜索上限，请用户调整条件 |
| 428 | PRECONDITION_REQUIRED | 补足版本/游标 |
| 503 | MAINTENANCE / DATABASE_BUSY / GENERATION_NOT_CONFIGURED | 按错误标志重试或完成配置；不声称请求已保存 |

## 2. 公开资源路由

下表字段为必填，`?` 表示可选。修改对象 `expected_revision` 与通用幂等规则同样适用。本文里 `source_ref` = `{source_id,source_version}`，`sense_ref` = `{sense_id,sense_version_id}`。不提供任意 patch、通用 publish 或物理删除历史的接口。

| 方法/路径（省略 /api/v1） | 正文或用途 | 成功结果核心字段 |
| --- | --- | --- |
| GET /health | 不含业务秘密 | status, schema_version, app_version, contract_version, mode |
| GET /client-context | 建立本机浏览器会话，Host/Origin校验及no-store | csrf_token；配套会话cookie由服务端签发 |
| GET /maintenance | 不含内容的维护状态 | mode, run_id?, run_status?, active_version, requires_rebuild, next_action? |
| POST /sources | name, location, source_version, license, usage_scope, checksum? | source_id, revision |
| GET /sources / GET /sources/{id} | 分页来源/来源元数据 | items 或 source |
| POST /sources/{id}/imports | source_version, entries[{external_entry_id,raw_word,raw_translation,pos?,tags?,aliases?,statistics?}], expected_revision | import_id, status |
| POST /banks | name（1–120字符）, note? | bank_id, revision |
| GET /banks / GET /banks/{id} | 摘要、数量、可测任务；不含答案 | items 或 bank |
| POST /banks/{id}/imports | words/text/file, source_refs, resolution_policy_version, expected_revision | import_id, status |
| GET /imports/{id}/items | status 筛选、分页 | items[{id,status,raw_input,candidates,resolved_sense_ids,revision}] |
| POST /import-items/{id}/resolve | action=adopt/exclude, sense_refs（adopt必填）, reason, expected_revision | item, revision |
| POST /banks/{id}/members/remove | sense_ids（1–1000）, expected_revision | bank_id, revision, removed_count；保留历史 |
| POST /banks/{id}/archive | expected_revision | bank_id, archived=true；成员和学习状态保留 |
| POST /banks/{id}/generation-jobs | sense_refs, generation_config_version, quality_policy_version, expected_revision | job_id, status, fixed_input_digest |
| GET /generation-jobs/{id} | 进度 | status, requested, published, rejected, pending, budget_usage |
| GET /question-candidates/{id} | 内容管理详情 | candidate, checks, provenance, disposition, revision |
| GET /quality-findings | job/candidate/status 筛选 | items, next_cursor |
| POST /quality-findings/{id}/resolutions | action=revise/exclude/regenerate/restore_validity, reason, content?（revise必填）, expected_revision | resolution_id, status, revision |
| POST /question-versions/{id}/retire | reason, expected_revision | version_id, status=retired, revision；preserve历史 |
| POST /question-versions/{id}/revisions | reason, content, form_relation=same/changed/uncertain, expected_revision | candidate_id, status=pending_checks |
| POST /question-versions/{id}/evidence-dispositions | action=disputed/invalidate/restore_validity, reason, expected_revision | disposition_id, rebuild_run_id, status；仅无活动会话 |
| GET /learning/senses/{id} | 无活动会话时读取词义/学习详情 | tasks[{family,evidence,memory,confusions}], projection_version |
| GET /learning/summary | 不含具体答案的学习摘要 | counts_by_task, evidence_conditions, projection_version |

普通词表支持 TXT 一行一个词，或 CSV `word,sense_id`（第二列可空）；文件走 multipart/form-data，其规范化幂等摘要由 metadata JSON 和原始文件 SHA-256 组成。CSV 只解析文字，不执行公式。source entries 是来源专用 JSON，不把普通词表当词义来源。

## 3. 会话与练习

| 路由 | 正文 | 结果 |
| --- | --- | --- |
| GET /sessions/active | 无 | session 或 null；包含 kind/id/status/revision、当前 cursor、last_ack_sequence |
| GET /practice-sessions/{id} | 无 | session、未答 public_question 或已答 feedback（仅原提交结果） |
| POST /practice-sessions | bank_id | session；同题库已有练习返回200恢复，其他活动会话409 |
| POST /practice-sessions/{id}/next | expected_cursor | `{status:"ready",session_revision,cursor,question}` 或 `{status:"unavailable",reason,counts}` |
| POST /practice-sessions/{id}/attempts | instance_id, option_id, submission_id, telemetry? | attempt_id, correct, correct_option_id, explanation, next_cursor, session_revision |
| POST /practice-sessions/{id}/end | expected_revision, reason=ended/abandoned | session, unresolved_unit_count |

cursor初始0，ordinal取保留时cursor，普通/纠错共用；另一个从1开始的normal_position只用于普通题探索比例，不能用ordinal判断1、5、9…的位置。普通实例成功保留时推进normal_position，响应重试不推进；有效答题才推进cursor。

`telemetry` 可选：`displayed_at_client`、`active_duration_ms`、`interruption_flags[]`、`display_event_id`，均为来源受限遥测；可附在attempts或考试response-events，与对应事实同事务入账，按display_event_id去重。未作答/未收到遥测时只知道reserved，不能推断实际展示；不得接收客户端correct/rating/due_at。训练提交一次后反馈，服务器唯一约束保证一次。

`public_question`：

```json
{"instance_id":"uuid","ordinal":0,"task_family":"meaning_recognition","stem":"Choose the meaning of river.","options":[{"id":"uuid-1","text":"河流"},{"id":"uuid-2","text":"山峰"},{"id":"uuid-3","text":"窗户"},{"id":"uuid-4","text":"沙漠"}]}
```

示例中的占位 UUID 仅用于说明结构。真实 ID 为合法 UUID，DTO 通过显式构造而不是从含答案对象删字段。

## 4. 考试

| 路由 | 正文 | 结果 |
| --- | --- | --- |
| POST /exams | bank_id, question_count, task_quotas?, time_limit_seconds?（60–14400）, tags? | exam_id, session_revision, deadline?, public_questions[], drafts[], last_ack_sequence=0 |
| GET /exams/{id} | 无 | 状态、固定公开题、服务器草稿、revision、水位、deadline；submitted时可提供result引用 |
| POST /exams/{id}/response-events | instance_id, action=select/clear, option_id（select必填）, client_event_id, client_sequence, expected_revision, rebased_from?={client_sequence,expected_revision} | event_id, accepted_sequence, current_revision, draft（无评分） |
| GET /exams/{id}/response-events/{client_event_id} | 已接受事件回执；不存在404不证明在途请求未接受 | event_id, client_event_id, client_sequence, accepted_revision, instance_id, action, option_id? |
| POST /exams/{id}/telemetry-gaps | expected_revision, reason=lost_queue/recovered_without_queue/client_gap | telemetry_incomplete=true, current_revision, last_ack_sequence；成功构成旧revision请求屏障 |
| POST /exams/{id}/submit | expected_revision, submission_id, last_ack_sequence | assessment_id, status=submitted, result_revision, result_url |
| POST /exams/{id}/abandon | expected_revision | status=abandoned；若已超时先自动交卷，返回已有结果 |
| GET /exams/{id}/result | 无 | 仅submitted：result_revision, total, correct, omitted, void, ratio?, questions[], response_analysis |

task_quotas的键只能是三个冻结task，省略键视为0；整份quotas省略才均分。客户端先持久保存动作与待发送正文；超时/断线时接受状态unknown，重试保持原ID/序号/revision/正文。仅明确not_accepted允许复用未占用的幂等键重排序号/revision，action/instance/option保持不变，附rebased_from；三次自动重排仍冲突则保留未保存提示。队列丢失先完成gap的CAS屏障，成功后从返回水位继续。完整协议见10 R01。结果中的首次正确性只在有可靠首次选择时返回，否则null；clear/noop不虚构首次答案。

首次统一交卷正确1、错误/未答0；报告修订若有 void，以有效题作分母并保留原报告链接。客户端只有已提交后能读取答案解析。考试设置没有计分、学习状态或 seed 的客户端覆盖字段。

## 5. worker 内部契约

以下路径带 `/internal/v1/generation`，全部POST、幂等，除了claim都需`{worker_id,item_id,fencing_token,input_digest}`。reserve/renew/candidates/complete要求当前有效租约；settle核对原预留身份，允许过期租约仅对账。租约/不可变调用身份代替通用expected_revision，候选处置仍按业务唯一性保护；内部协议不要求worker猜测job全局revision。候选保存、领取/续租及调用预算在维护期间可继续，正式发布暂停。

| 后缀 | 附加输入 | 响应与约束 |
| --- | --- | --- |
| /claim | worker_id, capabilities, limit（1–2） | items[{fixed_input,lease_until,fencing_token,attempt_number}]；无任务200空数组 |
| /renew | 当前租约身份 | lease_until；过期不能续回旧 fence |
| /calls/reserve | logical_call_key={attempt_number,stage,candidate_index,evaluation_index?}, network_attempt_id, profile_version, input_sha256, input_token_estimate, max_output_tokens | reservation_id, reserved_tokens, authorized_requests, budget_remaining；事务内检查租约/版本/逻辑尝试上限及job余额 |
| /calls/{reservation_id}/settle | outcome=response/network_error/unknown, provider_request_id?, response_sha256?, usage?={input_tokens,output_tokens} | call_status, accounted_tokens, job_budget_status；仅预算对账，迟到结果不获得发布权 |
| /candidates | attempt_number, candidate_index, parent_candidate_id?, content, generation_metadata（含generator/evaluator reservation_ids）, checks | candidate_id, disposition, published_version_id?；不重复发布或重复记usage |
| /complete | outcome=completed/needs_attention/budget_exhausted/failed, reason? | item_status, revision |

`checks` 至少包含程序检查版本及各门槛结果、两份语义评估的 evaluator profile/model/prompt_version、逐项 pass/reject/uncertain、理由、输入摘要；缺项不可发布。网络失败不会自动形成无效内容事实；已得到的候选必须保留 disposition。Go 在接收时重新执行确定性结构与身份校验；worker 自报 published 不被接受。

预算字段仅非负整数，max_output_tokens>0且不得超过固定profile。Go计算reserved_tokens=input_token_estimate+max_output_tokens，worker不能覆盖余额。每次实际发送对应不同network_attempt_id；reserve幂等回执不代表外部副作用可重放。settle独立幂等，候选引用的评估记录必须匹配输入/阶段/候选。完整账本及恢复规则见10 R02。

## 6. 验证范围

业务实现必须覆盖 05 的用例，以及：跨模式并发创建、发题与结束/retire争用、幂等早于revision、取消再选/noop、缺失client_sequence、deadline等时、自动交卷、管理接口答案泄露、组卷搜索耗尽与无解的区别、维护闸门漏写入口。当前 `make check` 验证依赖/fixture/配置契约，不代表这些 HTTP 行为已经实现或测试。
