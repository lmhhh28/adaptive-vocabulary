# Web 模块接口与验收契约

日期：2026-09-11。版本：`web-design-v1.1`。状态：待实现的增量契约，不代表下列新增接口已经上线。与 [11 交互设计](11-web-frontend-design.md)、[12 模块任务](12-web-implementation-plan.md) 配套。既有 API 的当前行为仍以 [前端联调指南](frontend-api.md) 为准。

## 1. 本次校对后确定的边界

| 决策 | 具体约束 |
| --- | --- |
| 单选即答 | 练习点击/1–4 激活选项立即请求 attempts；无待确认选中阶段 |
| 导航 | A/D 或题卡只改变查看位置；最新未答题不可跳过，已答题不可改答 |
| 自动前进 | 仅新收到的正确反馈可在可见页面停留 500ms 后前进；恢复/回看/手动导航后不再自动前进 |
| 本轮题卡 | 显示所有已出现题目的状态；超过 30 条分页，每页 30 条；可定位当前题，不能只截掉旧题却提供无效上一题 |
| 练习统计 | accepted attempt 数，包含 normal/early_review/correction；按原判定统计答对/答错，不当作长期掌握率 |
| 考试题卡 | 交卷前只显示未答/已答/当前/待同步，交卷后才显示对错 |
| 回看暴露 | 独立审计事实；本版不直接更新 FSRS、纠错或冷却，也不声称已排除提示影响 |
| 结束 | 先确认本地作答与展示事件，再 end；关闭页面不自动结束 |
| 实施状态 | 旧 Makefile 的 build/e2e 配方已经存在，旧冻结任务卡 planned 不是当前代码缺失的证据；新 W 模块仍是计划 |

## 2. W01：练习恢复与题卡

### 2.1 详情扩展

扩展 `GET /api/v1/practice-sessions/{id}`，在同一个只读事务快照中返回：

```ts
type AcceptedAnswer = {
  question: PublicQuestion;  // instance_id, ordinal, task_family, stem, options
  selected_option_id: string;
  feedback: AttemptFeedback;
};
type AnswerRecord = {
  instance_id: string;
  ordinal: number;           // 服务端从 0 开始，界面 +1
  correct: boolean;
};
type PracticeDetail = {
  session: Session;
  current_question?: PublicQuestion;
  last_answered_question?: AcceptedAnswer;
  summary: {
    counts_scope: 'accepted_attempts';
    answered_count: number;
    correct_count: number;
    incorrect_count: number;
  };
  recent_records: AnswerRecord[];  // 最近 30 条，按 ordinal 升序呈现
  has_older_records: boolean;
};
```

`answered_count = correct_count + incorrect_count`，按本 session 的唯一已接受 attempt 计算，重放不增加计数，未答不计错。纠错也算本轮一次作答，报告明确是全部作答口径。后续质量处置不改写这些历史原判定；有效学习证据由学习报告另行说明。

若当前 cursor 有未答实例，返回 current_question，不附带旧答案正文；否则查询最近一个已接受 attempt，连接当时实例快照返回 last_answered_question。不能只查 ordinal=cursor；正常已答未 next 时最近答案是 cursor-1。last_feedback 可为兼容保留，但新前端只使用完整对象。所有返回 no-store。

session.cursor 是服务端已完成位置；activeQuestion 是已保留最新实例；viewOrdinal 是客户端阅读位置。客户端不能通过传旧 ordinal 改写 cursor。

### 2.2 题卡分页与单题回看

| 新增接口 | 请求与响应 | 不变量 |
| --- | --- | --- |
| `GET /api/v1/practice-sessions/{id}/answer-records` | `before_ordinal` 可选且排他，`limit` 默认 30、1–100；返回 `{items, next_before_ordinal, has_more, summary, session_revision}` | 默认最近一页，items 升序；游标取本页最小 ordinal；0 必须可用，禁止真假值判断丢掉首题 |
| `GET /api/v1/practice-sessions/{id}/answered-questions/{ordinal}` | 返回 AcceptedAnswer | 必须属于该 session 且已有 accepted attempt；未答/未来/错误 session 统一 404，不返回任何答案字段 |

题卡分页只查状态，不批量拉取解析。回看单题使用单题接口；已在本地的原反馈可直接读取缓存（前端在会话生命周期内缓存已获取的 AcceptedAnswer 对象，HTTP 响应一律标明 Cache-Control: no-store）。GET 不新增 attempt、预留实例、曝光或其他写事实。若另有活动作答会话，禁止读取其他会话的答案正文；同一活动练习允许本轮已答回看。当前题卡位置在分页变更后仍可通过「当前题」定位。

### 2.3 显示事件：本版只记审计，不偷换学习口径

当前 `reported_display` 没有可用写接口、没有 display_event_id 的完整去重，`DeriveExposureLabels` 又遍历所有 kind；不能直接把新回看插入原 exposure_event 并假定无影响。本版新增独立 `practice_feedback_view` 表，由 W01 独占迁移，保留未来解释数据。既有 exposure_event、eligibility matrix、FSRS grading 与冷却逻辑保持原规则。

新增 `POST /api/v1/practice-sessions/{id}/feedback-views`：

```json
{
  "client_event_id": "UUID",
  "instance_id": "UUID",
  "presentation": "history",
  "client_displayed_at": "2026-09-11T09:00:00.000Z"
}
```

- presentation 仅 `history` / `restored_feedback`；新提交首次反馈已有 feedback_available，不重复计作回看。每次从另一题进入反馈正文，或刷新后实际恢复反馈，生成一个新展示 ID；组件 rerender/StrictMode 重挂载不算新展示。
- 必须存在同 session 的已接受 attempt。题目版本、词义、单词、form 由服务端从实例关系解析，不能相信浏览器传入或让浏览器猜 ID。
- `Idempotency-Key = client_event_id`；首次成功 201，原 key 原正文重试返回同一事件和时间，原 key 换正文 409 `IDEMPOTENCY_CONFLICT`。同时建立事件 ID 唯一约束，不能只在 UI 去重。
- 服务端返回 `{view_id, client_event_id, event_sequence, recorded_at}`；recorded_at 为服务端 RFC3339 时间，客户端时间仅作辅助原始信息。延迟到达不能回写过去的服务端排序。
- 允许对已结束练习补交已经发生的本轮展示记录，仍检查会话/实例归属；响应只含回执元数据，不借写接口泄露答案。维护状态按现有写入闸门处理。
- 不递增 session.cursor、session.revision、normal_position；不创建 attempt/review、不推进 3/5 词间隔、不清除待巩固。原始展示记录可追溯，报告不宣称这种作答没有受到回看提示影响。
- 以后若要用回看改变普通证据资格或选题排序，必须另增 observation policy、回放与对照验收。本次任务不把尚未设计的统计解释混入前端迁移。

客户端先把展示动作存入持久队列再呈现回看内容，后台按序确认；不为每次读取额外设置等待动画。在队列尚未确认时可继续只读浏览，但新的作答、next、end 要等待它排空；断线或本地存储不可用时保留明确同步状态，不能声称记录已保存。快速连按 A/D 切换回看时，客户端可在当前题停留达到防抖门槛（如 200–300ms）后才向 outbox 登记一次有效展示，避免短时间内产生无意义展示堆积阻塞新作答。

### 2.4 写入不确定性的统一规则

W00 提供 IndexedDB outbox 存储接口，W02/W03 各自定义操作语义与处理器。练习选项首次激活后同步锁定，再持久化以下不可变 envelope：

```ts
type PendingWrite = {
  schema_version: 1;
  operation_id: string;
  kind: 'practice_attempt' | 'practice_end' | 'feedback_view';
  session_id: string;
  method: 'POST'; path: string;
  idempotency_key: string;
  body_text: string;         // 首次序列化正文；重试逐字复用
  state: 'queued' | 'sending' | 'unknown' | 'confirmed' | 'rejected';
  created_at: string;
};
```

不把 CSRF token 固定进正文；明确 403 CSRF_REJECTED 后刷新 `/client-context`，原 key/body 重试。网络断开、超时、响应丢失不是拒绝证据，原动作保持锁定，刷新优先恢复 outbox，再读详情。练习 attempts 没有单独回执查询时，以原请求重放确认，不发明一个 404 查询来证明“没有提交”。得到明确未接受错误后才按具体错误恢复；不同选项的 ALREADY_ANSWERED 用详情/历史还原第一次结果。

本地持久化失败时不发送新作答；提示存储不可用并允许恢复。读取终态不删除 unknown 记录，先确认已有动作与历史匹配，再完成本地收敛。

## 3. W04/W06/W07：管理资源发现

此前设计让生成/质量页“先填已知 ID”，无法形成完整用户路径。新增能力明确分配如下，全部是待实现接口：

| 所有者 | 新增或扩展接口 | 最小字段 |
| --- | --- | --- |
| W04 | `GET /api/v1/content/sense-versions?bank_id=…&cursor=…&limit=…` | `sense_id, sense_version_id, lexeme_id, canonical_form, definition, status` |
| W04 | `GET /api/v1/content/question-versions?bank_id=…&cursor=…&limit=…` | `question_version_id, sense_id, form_id, task_family, status`；默认不含答案 |
| W06 | `GET /api/v1/generation-jobs?cursor=…&limit=…` | `job_id, status, requested_count, published_count, created_at` |
| W06 | 扩展 `GET /api/v1/quality-findings` 分页 | 保留现有 limit 调用兼容；新增稳定 cursor/has_more 与题目或候选引用 |

分页统一返回 `{items, next_cursor, has_more}`；limit 默认 30、最大 100，opaque cursor 编码稳定排序键，非法 cursor 返回明确 400。版本查询按稳定 ID，任务/异常按创建时间+ID；不能用 offset 翻滚造成重复遗漏，也不能按 display text 拼造 version ID。活动会话期间按现有防泄题边界拒绝管理详情/候选查询。W04 的 content 发现接口统一由 `internal/bank/handler.go` 注册挂载并维护，与现有 `/banks`、`/sources` 路由共用同包上下文，避免分散。

W07 复用上述发现入口与既有 evidence-dispositions、retire、finding resolution、maintenance。finding resolution 仍只是「已记录处置」；不承诺修改内容。创建修订版本当前 404，仍不提供该按钮。维护重建/备份恢复仍用既有 CLI，不新增无实现依据的管理按钮。

## 4. 启动与 E2E 的共同约束

以下命令由 W00 实现后才可执行，本次只交付计划工具：

```sh
make web-build
make web-run MODULE=W02 DATA_DIR=artifacts/manual/W02 LISTEN=127.0.0.1:0
make web-e2e MODULE=W02
make web-e2e MODULE=all
```

`web-run` 默认使用构建后的真实 React/Ant Design 资源，由 Go 同源提供；根据模块选择入口与 fixture/worker，输出 ready JSON 和可打开 URL。动态端口避免模块冲突，手工指定同 data-dir 时须拒绝双实例。开发 Vite 可作为可选模式，但模块验收必须覆盖构建产物通过 Go 提供的路径，不只跑 Vite。

`/client-context` 在根路径，`/api/v1/health`、`/api/v1/maintenance` 在 API 前缀下。开发代理必须正确转发这三类路径及 Cookie/CSRF/Host/Origin，不能只代理 `/api` 导致初始化 404，也不能关闭 CSRF/Host 校验掩盖配置问题。

新 `e2e/web_runner.py` 使用 W 计划登记的 case 列表；复用已有 runner 的进程组隔离/硬超时机制，不改旧 Mxx 的发现语义。每次结果放在 `artifacts/web-e2e/<run-id>/<module>/<case-id>/`，顶层 result.json 标识本次测试范围、代码版本、二进制/资源哈希和所有 case。

每个模块可以在它的依赖完成后独立验收。W01 是 API 模块，E2E 为真实 HTTP → Go → SQLite；所有 UI 模块必须有浏览器 E2E。W06/W07 涉及内容生成时使用真实 LangGraph worker，通过本地确定性 HTTP provider 替代外部付费模型；Go、业务数据库、worker 本身不能被 mock。不调用真实付费模型作为默认验收。

### 4.1 必测数量与证据

任务 JSON 中的 acceptance 是必测 case 清单。缺文件、漏 case、0 个 case、skip、错误退出、超时或缺必需证据都失败，不允许目录存在就标记完成。`run(context)` 必须执行断言并显式返回结果。

| Case 类型 | 必须保留的证据 |
| --- | --- |
| 所有业务 case | `processes.json`（命令、PID、端口、绝对 data-dir、退出码）、服务日志、实际 ready/health、`state-summary.json`（相关数据库不变量，不含凭证） |
| browser | 上述内容 + Chromium 版本、`trace.zip`、至少一张有意义截图、console/pageerror 记录、应用资源 manifest/hash、动作对应的 HTTP 状态摘要 |
| api | 上述内容 + 请求/响应协议摘要（脱敏）及数据库关联断言 |
| worker | browser/api 证据 + worker/provider 独立日志、检查点位置、job/candidate/published 关联事实 |
| harness 自检 | 父进程记录子进程的预期失败结果；只用于检验框架，不能替代模块业务 case |

每次 server 启动使用不同 log_dir，例如 `server-01/`、`server-02/`，禁止恢复测试覆盖首次日志。每 case 独立数据库、浏览器 context、provider 端口和 checkpoint；不复用用户运行中的服务或学习数据。所有子进程在 finally/进程组回收，失败仍保留证据。

模块 result.json 至少含 `module`、`design_version`、`code_revision`、`status`、`passed`、`failed`、`skipped`、`cases`；每个 case 含机器清单登记的 `id`、`status`、`artifacts`、`details`。只接受本模块全部且不重复的 case ID。完成任务时 verification 保存 `{result_path, code_revision}`，计划校验器会核对结果范围与通过数量；实际证据文件内容仍由 web runner 校验和审阅。

证据校验器检查路径属于当前 case、文件存在且非空、要求的证据类型齐全；版本来自实际运行进程和构建 manifest。文件存在不等于自动证明业务正确，case 仍必须通过真实行为和数据库关联断言。

### 4.2 允许的准备与故障注入

- 正常流程优先使用真实 API/seed。测试可在自己的隔离库准备静态题目内容、控制时钟或维护状态；不能预写该 case 应由用户行为产生的 attempt、交卷结果或生成发布事实。
- 正常二进制至少承担每个 UI 模块一条成功业务路径；测试二进制只用于受控时钟、崩溃点等故障 case，测试路由不得暴露在普通构建。
- 浏览器拦截仅可用于丢弃真实服务已经接受的响应、断网或延迟传输。必须先真实转发并在 SQLite/回执中证明是否接受，不能用伪造的成功 JSON 代替应用服务。
- 状态机可用假定时器精确验证 500ms 与竞争；浏览器用例使用条件等待及容差，验证正确最终自动前进、错误持续停留、手动前进无额外固定等待，不用脆弱的精确 500ms 墙钟断言。
- M10 旧浏览器用例仍依赖旧按钮/键盘。W08 必须同步迁移测试到新行为后再跑全量，不允许仅保留旧页面让旧用例通过。
