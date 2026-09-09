# 首版冻结设计与审查结论

版本：**1.1.0**。冻结日期：**2026-09-08**。适用范围：单人、本机、在线作答不依赖 LLM 的首版。本版在1.0.0基线上修复独立审查的六项问题，实施入口为[任务拆分](09-implementation-plan.md)，精确修订规则见[10](10-review-resolutions.md)。冻结表示实现可以据此开始，不表示业务功能已经完成，也不表示学习效果已经验证。

## 1. 冻结基线和变更规则

- 产品边界遵循 [P01–P13](01-product-and-decisions.md)。Go 模块化单体负责业务事实、评分、投影、任务租约；Python LangGraph 只负责离线生产与自身检查点。
- Go 标准库 `net/http` 路由、`html/template` 页面、`embed` 静态资源；浏览器使用原生 ES modules 与 CSS。服务同源，不增加前端框架、Node 构建链或第三方 CDN。此选择不限制后续 UI 设计。
- SQLite 纯 Go 驱动与 FSRS、Python 包使用 [依赖清单](07-dependencies-and-development.md) 和提交的锁文件。Go 与 Python 的 SQLite 必须分别实测。
- 业务契约由本文、[08](08-http-contract.md)及[10](10-review-resolutions.md)组成：10定义六项修订的状态/资格，08定义传输，本文定义其余全局约束；01–07为概述。所有路径加 `/api/v1`，内部接口为 `/internal/v1`。策略数值和资格矩阵唯一来源为 [policy-v2.json](../config/policy-v2.json)。冲突必须修正文档，不能由实现自行选择解释；09定义实施及验收，不覆盖业务语义。
- 变更身份、事件资格、评分、冷却计数、排序或 FSRS 参数必须增加相应版本并记录迁移/回放方案；改标题或排版不升级策略。禁止在原版本下调整运行参数而不留痕。
- 当前交付是冻结文档、配置、来源样例、锁文件与依赖验证程序。完整 HTTP 服务、SQL 迁移、浏览器界面和生产 worker 属于后续实现，不冒充已有功能。

## 2. 本轮审查处置

| 优先级 | 草案缺口及具体风险 | 冻结处置 | 实现验收 |
| --- | --- | --- | --- |
| P1 | 练习/考试各自创建，可能同时更新同一学习单元 | 共用 `session` 表与单活动唯一索引，无自动租期失效 | 两模式并发创建只能成功一个 |
| P1 | `/next` 计算期间结束会话或停用版本 | 提交保留事务内重新校验游标、内容、题库及投影版本 | 迟到计算不能创建非法实例 |
| P1 | 网络重试与真实改答混淆、交卷边界不确定 | 幂等优先、revision CAS、客户端序列逐条确认、交卷事件水位 | 重试不新增事件，冲突不覆盖草稿 |
| P1 | 管理查询、错误体或缓存绕开发题 DTO 获取答案 | 答题 DTO 白名单、活动会话期间内容明细禁用、统一错误体 | 交卷前全链路不返回答案 |
| P1 | 内容错误、重建写入及活动投影切换不明确 | 争议隔离、追加修订、统一维护闸门、新投影原子切换 | 在线结果与指定策略回放一致 |
| P1 | 运行时及传递依赖未固定 | 项目隔离工具链、`go.sum`/`uv.lock`、实际 SQLite 和检查点验证 | 干净同步后依赖检查成功 |
| P2 | 冷却“出现”没有事件定义、刷新可绕过 | 以服务端保留实例为保守曝光事实；间隔由有效提交推进 | 重试/退出不累积间隔 |
| P2 | 语义检查只写原则，无发布状态机与成本上限 | 两个分离评估调用、全项 pass、有限重试、异常停住 | 错义/多解/超预算均不可发布 |
| P2 | 汇编词库字符串被误当标准词义 | 原创 fixture 先行；ECDICT 仅作有来源的候选数据 | 无来源、冲突、歧义不能自动确立 sense |
| P2 | 备份完成标志、附件和检查点边界不清 | 临时目录、快照、SHA-256 清单、原子完成、恢复对账 | 新目录恢复演练通过 |

1.1.0独立审查新增处置：R01明确拒绝事件重排及gap屏障；R02逐次外部调用的Go持久预算账本；R03按重建原因决定abort能否解闸；R04排除当前实例并按观测cutoff派生曝光；R05提前复习资格矩阵；R06可组卷正例及无解负例。逐项触发场景、字段和验收ID见10，实施任务映射见09。

## 3. 身份、存储和运行边界

业务ID由服务器生成随机UUID；客户端事件ID、submission ID及worker的network_attempt_id为调用方生成的UUID，详见10 R01/R02。开发固定种子身份仅用于显式fixture导入。时间保存UTC Unix微秒；JSON使用RFC3339 UTC。显示日期/跨日期统计采用配置固定的`Asia/Shanghai`，变更时升级报告策略；FSRS间隔按实际秒数计算，不按本地午夜截断。

词条规范化 `lexeme-normalization-v1`：Unicode NFC、去首尾空白、内部空白压为一个空格、英语大小写折叠；保留连字符、撇号和变音字符。大小写会改变词义的条目进入异常；同形不同词性/词义共用 lexeme，派生词不合并。屈折词仅在来源提供且无冲突的显式 `lexeme_alias → lexeme_id` 映射下归一，不做启发式词干化。归一冲突保留候选，不自动覆盖。

`sense_id` 表示固定词义范围；一个词可以有多个 sense。释义措辞修订创建 `sense_version`，改变词义范围必须新建 sense。学习状态主键始终是 `(sense_id, task_family)`。已发布题目所引用的 sense_version 永久保留。

`option_id` 是题目内用于选择的UUID；`option_identity_id` 是跨题混淆统计所用的不可变语义UUID，二者分开。明确sense引用的选项按canonical key唯一登记并复用：`option-normalization-v1 + language + NFC/空白规范化显示文本 + 排序后的sense_id集合 + 排序后的source_id/source_version集合`；key以规范JSON的SHA-256存储，冲突仍比较完整字段。不能每个候选都生成新的语义UUID。无明确sense的中文选项还带form lineage作用域，不跨form合并统计。选项顺序不改变语义身份；四个选项的语义身份和规范化文字均不得重复。

`question_form` 的自动去重键包括sense_id、task_family、NFC/空白规范化题干和无序选项canonical内容key集合（不包含随机UUID；无sense选项去掉统计用的form作用域）；它只识别明确重复。措辞等义修订须显式携带parent_form_id和质检依据才能复用form，不确定则进入异常。新候选经过重复及语义检查才取得新form ID；答案键修正不制造新form。目标义范围、语义条件或干扰语义改变才是新形式。

数据库必须实现以下约束，迁移实现不得弱化：

| 对象 | 唯一性/关系 |
| --- | --- |
| `session` | `kind=practice/exam`；对常量 `(1) WHERE status='open'` 建唯一部分索引，跨模式最多一个活动会话 |
| `question_instance` | `(session_id, ordinal)` 唯一；保留完整快照和版本 FK；训练最多一个未回答实例 |
| `attempt_event` | `instance_id` 唯一；选择/取消事件放在另一表，不能重复生成 attempt |
| `response_event` | `(session_id, client_event_id)`、`(session_id, client_sequence)` 唯一；事务内递增 session revision |
| `idempotency_record` | `(operation, resource_scope, key)` 唯一；保存请求摘要、状态、响应或响应引用 |
| `review_event` | `(attempt_id, observation_policy_version)` 唯一；不同投影版本不修改旧 review |
| 内容/来源 | 外部条目 `(source_id, source_version, external_entry_id)` 唯一；`bank_sense(bank_id,sense_id)` 唯一 |
| 投影 | `(projection_version,sense_id,task_family)` 唯一；混淆另加 option_identity 与统计组 |
| 生成 | item 固定目标/输入摘要；`(item_id, attempt_number, candidate_index)` 唯一；租约具有递增 fencing token |
| 调用预算 | `(job_id, network_attempt_id)`唯一；reservation_id唯一；每个logical_call_key的尝试数在事务内限制；每个reservation唯一最终settlement及摘要，状态/余额条件更新同事务 |
| 重建 | 单一projection_control与run条件更新；predecessor_run_id唯一后继；成功目标version不可覆盖，重复恢复命令返回原结果 |

Go默认绑定`127.0.0.1:8080`，只接受127.0.0.1，允许显式端口及测试端口0；实际端口随ready JSON返回，便于隔离并行验收，见09启动契约。首版不提供公网部署。单业务进程及写库CLI共用数据库之外的进程文件锁；服务运行时写库CLI返回SERVICE_RUNNING。所有SQL连接设置`foreign_keys=ON`、`busy_timeout=5000`、`synchronous=FULL`，业务写连接池`MaxOpenConns=1`，独立只读池不改变单写事务约束。读取实际`journal_mode=wal`和`sqlite_version()/sqlite_source_id()`，两种运行时SQLite均至少3.51.3。

浏览器使用同源页面与 API，校验 Host 和写请求 Origin，使用本机会话 CSRF token，关闭跨域 CORS。worker 内部接口仍绑定 loopback，使用启动时生成的独立高熵 bearer token（文件权限 0600）；浏览器不可得到此 token。应用以纯文本转义方式展示题干/模型内容，不渲染外部 HTML。不引入账号系统。

## 4. 会话、提交、排序和反馈

`practice: open → ended | abandoned`；`exam: open → submitted | abandoned`，终态不可恢复为 open。关闭页面不结束会话，也不暂停考试时限。活动状态没有自动租期：通过 `GET /sessions/active` 恢复；用户明确结束/放弃才释放唯一槽位。定时考试到期必须提交而非放弃。多标签通过同一 revision/游标冲突协议收敛。

会话cursor初值0，表示下一实例的0-based ordinal；ordinal对普通/纠错共同递增。另存normal_position，从1开始，仅普通题成功保留时递增并永久写入实例（纠错为null），用它判断1、5、9…探索位置。两种序号不能混用。

训练发题计算可在事务外进行；保留事务再次检查活动状态、expected_cursor、题库成员revision、发布状态和活动投影版本。首次实例保留必须在同一事务追加唯一`reserved` exposure（instance_id+kind唯一）；恢复、同游标重试和重复响应只返回原实例，不重新计reserved或移动其时间。实际展示遥测按独立display_event_id去重，可缺失，不能替代reserved。游标只在有效答题或明确作废实例后推进；不能靠反复next跳题累积间隔。

训练提交事务先查幂等记录和实例已有attempt，再按统一维护/状态顺序校验；同实例同选项重复返回原反馈，不同选项返回ALREADY_ANSWERED。评分、事实、review/投影、纠错队列、游标及唯一feedback_available曝光在一个事务内完成；该曝光只表示服务器已准备反馈，不证明页面展示。非法/缺失/多个选项和未知字段拒绝；成功后无论对错都返回答案与解析。考试组卷的所有reserved事件与固定实例同事务创建，交卷/结果恢复不会重新追加reserved。

考试选择事件是 `select(option_id)` 或 `clear`。客户端对整张卷维护从1开始的连续client_sequence，持久保存待确认队列并逐条确认。接受状态未知时使用原ID/序号/revision/正文重试；只有服务端明确返回not_accepted的事件可保留动作和ID，恢复后重新分配下一序号与revision，最多自动重排3次，详见10 R01。服务端按事件身份去重后验证revision和序号，不能返回其他ID占用该序号的回执。重复选择当前选项及取消未选题记noop，不增加改答数；clear后再选择属于改答。队列丢失必须先完成telemetry-gaps的revision屏障，再从服务器水位继续；整卷按最终草稿评分，但不产生FSRS review。

手动交卷必须等待所有选择确认，携带 expected_revision 和 last_ack_sequence；服务端只冻结已经接收的水位，不接受夹带最终答案覆盖历史。定时交卷与事件写入在同一序列化事务入口处理：`server_now >= deadline` 时先执行超时交卷，迟到选择不纳入成绩，返回 `EXAM_SUBMITTED`。自动交卷标记时间线不完整（服务端无法知道是否有未发送选择），因此该卷不产生 FSRS review，仍保留评分/暴露。重启后在接受任何作答前完成过期考试结算。

业务事实全局序号在写事务内分配，不是客户端时间。考试交卷后，每题合格 review 使用唯一一次有效 select 的服务器接收时间；按 `(review_time,event_sequence,attempt_id)` 回放。单活动会话和考试目标 lexeme 去重保证交卷前没有同 unit 的其他学习写入。放弃考试保留已知暴露/选择，不评分、不创建 Again。

公开发题对象仅含 instance_id、ordinal、task_family、stem、options[{id,text}]。答案键、解析、quality/candidate 内部字段在提交前不可出现在任何发题、恢复、错误或日志载荷中。答题/报告返回 `Cache-Control: no-store`；日志只记 ID/耗时/错误码，不记请求正文。活动作答期间禁止导出题目答案、候选明细、词义明细及内容编辑接口，返回 `SESSION_ACTIVE`，以免管理入口直接旁路提前查看答案；来源/任务进度等不含答案的摘要仍可读。单机用户可自行查看磁盘，此产品规则不宣称防作弊。

## 5. 确定的学习和选题策略

版本：`observation-v2`、`selection-v2`、`grading-v1`。普通证据及间隔资格严格按10 R05和policy-v2矩阵：normal与early_review均推进普通位置和两类间隔，可靠考试计入普通证据但不推进训练间隔，纠错不更新待巩固标志。采用 FSRS-6 的库默认 21 权重，参数快照见 config；desired_retention=0.90，maximum_interval=36500 天，short-term 开启，fuzz 关闭，learning steps=[1,10]分钟、relearning steps=[10]分钟。完整保存库Card字段（包括学习步数、elapsed/scheduled days），不能只保留S/D。due时间是排序依据，不能越过冷却/纠错次数约束。服务器显式传入 review 时间，不使用库内隐式当前时间。R/S/D 只表示模型调度状态。

普通/提前训练正确 Good、错误 Again；有效纠错每次各更新一次；可靠考试单次选择在交卷时更新一次；考试改答、遥测不完整、未答、放弃、争议/无效题均不生成 review。表现和混淆分组按 03 定义。被排除的作答/反馈仍保留暴露 gap，不能挪动 last_review_at 伪造模型更新。RT 只在展示/开始/结束遥测完整、未中断且有限非负时计入描述统计，否则为 null；RT 不影响评分或 FSRS。RT 缺失不等于考试选择时间线缺失，两种资格分别判断。

普通位置编号从 1 开始，1、5、9…优先未测组，其余优先 due 组；每组无候选时换另一组，都无候选才进入 early_review。纠错不占普通位置。每次先从可用内容构建 unit，再应用冷却，最后排序；同排序稳定使用 sense_id/task 固定枚举序/版本 ID，首版练习不随机抽目标。

| 组 | 排序元组（由左至右） |
| --- | --- |
| 已到期 | 最近普通有效观测错误优先、due_at 升序、合格普通证据数升序、最早普通观察时间（缺失最前）、sense_id、task |
| 未测 | 同 lexeme 已测任务数升序、sense_id、task；只有纠错或改答仍是未测 |
| 提前 | due_at 升序、普通证据数升序、sense_id、task；明确标记 early_review |
| 可执行纠错 | 最近触发错误的业务序号升序、sense_id、task |

词频/考试重要度首版不参与排序：保留原值与来源用于展示和后续版本。形式排序：本会话未用form优先、最久未保留form、包含至少两次普通错选的混淆身份优先、form_id；同时记录机会数，不能解释为难度。纠错已用集合严格按form_id，任何同form的新version、修正答案键或洗牌都视为已用；只选该unit本会话未作答的新form，否则blocked_content。用足两次重试需要初次题外另有两个合格form。

冷却以服务端保留实例记 `reserved` 曝光，不宣称已看见；间隔仅由此后**有效提交**的其他不同 lexeme 推进。同实例重试不计，退出未答不计推进。普通题距该 lexeme 最近任意保留实例（含纠错）之后，须完成 5 个不同其他 lexeme 的普通题；纠错本身不推进普通间隔。纠错距本 unit 最近错误提交后，须完成 3 个不同其他 lexeme 的题（普通/纠错皆可）。选择下一题时当前未答实例始终阻止重复保留。

冷却历史跨会话保存，切换/刷新不重置。小题库无合法候选时 next 返回 `NO_ELIGIBLE_QUESTION` 及 `cooldown/content_missing/exhausted` 计数；可结束本轮。新会话距离该 lexeme 最近保留/相关反馈已满 24 小时可按 `selection-v2` 重新进入普通候选，此时间释放只是一项调度默认值，不赋予独立证据标签。不会静默在同会话缩短 3/5 间隔。

纠错每 unit/会话一个活动 item，保存触发链与最后一次错误；普通/提前错误可开启，纠错错误推进同一个 item，不分叉。每会话最多两次纠错提交，成功标记 resolved；用尽为 exhausted，缺新 form 为 blocked_content；退出转 deferred。最近普通错误保留待巩固标志，纠错成功不清除；冷却、due 和以后普通正确观察决定后续练习。

已知相关暴露使用完整instance中明确引用的目标/英文选项lexeme，以及反馈的sense/lexeme建索引；不从中文同文推断隐含词。历史提示只查其他instance且严格早于当前观测(observed_at,event_sequence) cutoff的曝光；本实例reserved和本次反馈不算历史提示。相关reserved/feedback近24小时加近期提示，same_form_reserved不设时间窗口；考试按select时刻而非交卷/回放时刻截断。训练提交及考试交卷分别产生唯一feedback_available，结果重读不重复追加；详细定义见10 R04。未建模暴露gap持续到下一个有效review，相关历史不删除；24小时不证明心理独立性。

考试与练习分开组题。题量 1–200，任务配额非负整数且总和等于题量；未指定时按三个任务尽量均分，余数按固定 task 顺序分配。组卷在 lexeme 与 task 配额间做最大流/二分匹配，不能贪心选完再重复补足；有多 sense 的 lexeme 使用稳定随机优先级选择可行边，保存算法版本/seed 和最终快照。首版考试使用标准库确定版本的 PCG，保存两段 uint64 seed，所有候选预先按 ID 排序。

跨题显著提示规则冻结为：选中目标lexeme不可作为卷内另一题的英文干扰项；语境句内不可明文出现其他目标规范词形/显式alias（分词匹配）；匹配后冲突搜索最多10000个状态。无可行解返回EXAM_CONSTRAINT_UNSATISFIED；达到上限返回ASSEMBLY_LIMIT_REACHED，不能把尚未找到解说成无解。保存候选快照/输入版本、选中lexeme/sense/task/form、seed及排除码（content_missing/quota/cross_question_constraint/lexeme_dedup/random_priority），不重新生成内容解释历史。完整中文语义提示不承诺自动消除，未随机等值试卷不作严格分数可比声明。

## 6. 内容来源、生产与自动发布

首批可核验数据采用项目原创[基础fixture](../fixtures/content-v1.json)与[组卷补充fixture](../fixtures/exam-assembly-v1.json)，覆盖同词多义、三任务、替代形式、无效题及三题型共同配额的可行/无解场景；两套原创来源分别登记，频率/真题次数为null。fixture只是开发基线，不冒充完整CET-6词库。

扩充来源冻结为 ECDICT 的版本化导入适配器，定位/许可和处理边界见 [来源清单](../config/content-sources.json)。`tag` 字段中的 cet6 仅表示该快照标签，不代表完整官方考试大纲。ECDICT 不直接形成标准 sense 或可发布题目：只将无冲突、单词性/单一清晰释义的条目经版本化规则及质检接纳，多义混排进异常；不随意按逗号拆义。首次全量导入必须存 SHA-256、行数、重复数、标签命中数、未解析数与许可文本。

生成默认关闭，`fixture` 模式无需模型账户、联网或额度。生产接入采用 HTTP JSON provider adapter，两个独立 profile（generator/evaluator）必须显式配置 base_url、model、key 环境变量名、请求超时、max_output_tokens 和单作业预算；模型不得使用隐式 latest，保存供应商返回的真实模型 ID（可获得时）。配置不是学习接口输入，普通用户不需理解模型名。缺 key/profile 时返回 `GENERATION_NOT_CONFIGURED`，不在本轮擅自创建收费服务。

每 unit 默认请求 3 个 form；不足可部分可用，不以凑数降低门槛。item 租约 120 秒、30 秒续租，所有接收请求带 owner、递增 fencing token 和 input digest；数据库时间为准，过期结果拒绝。worker checkpoint thread_id 固定 item_id，生成前后检查租约，重放节点仍使用相同 attempt identity。

语义修订最多2次（含初次最多3代候选）；每个逻辑调用最多3次网络尝试，超时45秒、退避1/2秒加有界抖动。并发item=2。每作业最多100个unit、100次请求授权及200000已知/预留token占用；每次实际发送前通过Go事务预留，网络尝试不能共用预留。崩溃/换fence后未知调用保持扣减，新发送重新预留，usage缺失不归零。预算账本、迟到结算和备份恢复按10 R02；实际usage超过估计则完整记账并标记budget_overrun、停止新授权，不承诺供应商账单绝对上限。SDK不得隐藏重试/重定向。

发布要求：结构/来源/身份/唯一答案键门槛全 pass，且**两个分离语义评估调用**均逐项返回 pass：target_meaning、unique_answer、grammar、naturalness、explanation_consistency、distractor_validity。评估器不看生成器自评分；两次可使用同一显式 evaluator 模型但不是统计独立保证。结果枚举 pass/reject/uncertain，分歧为 conflict；reject 可有限修订，uncertain/conflict 进异常，不基于平均分发布。Go 验证评价结构及版本后幂等转为 auto_passed/published。人工只处理异常；来源缺失、结构非法不能靠人工绕过。

开启批量自动发布前，在固定的有效/无效检查集上验证：所有已知错义/多解/无解/答案冲突样例必须拦截，合格样例误拦截数和比例必须报告；先通过至少每类 5 个负例、合计至少 30 个负例与 15 个正例的门槛。本仓库 fixture 是最小开发集，不等于这项生产验收已经完成。评估器或 prompt 变更需重跑；上限不代表真实题库零漏检。

## 7. 停用、修订、重建及备份

内容处置分三种：纯文案/来源修订 `preserve`，仅阻止新实例引用旧版、历史按原快照；疑似错误 `disputed`，阻止新实例、相关新提交不进主统计/FSRS，已有影响需在维护期重建隔离；确认错答案/多解/无解 `invalidate`，追加 event_revision 排除对应证据/FSRS/混淆。首版不自动把错误答案改成新 review，也不使用原事实补写新答案。

`disputed/invalidate/restore_validity` 处置请求在活动会话存在时返回SESSION_ACTIVE，整个处置事务不写任何状态/修订；此错误属于管理操作，不是答题提交。先结束/提交/放弃会话才能原子登记处置、进入维护闸门及固定重建水位。因而首版不可能在同一活动会话中把已保留题改成disputed/invalidate；原题按当时有效快照正常提交，之后通过修订重建排除。普通retire只禁止未来保留，已有活动快照继续评分和按原规则产生观测。

已提交原assessment_result永久不变；disputed先显示待核实并从主证据/FSRS隔离，invalidate生成报告revision，列出void题及有效题得分/分母（全void比例null），保留原分数。确认争议无误追加restore_validity并重建。任何时刻新实例都不能引用disputed/invalidated/retired版本；新选择和内容处置争用由同一写事务串行化。

重建须无活动会话，数据库内 `projection_control` 单例保存 mode(active/rebuilding)、run_id、active_version、水位；进入维护模式与冻结水位在一个事务中完成。所有作答、曝光、评分/内容证据修订写入口和学习相关导入检查同一闸门，返回 `MAINTENANCE`，不排队接受后声称成功。生成候选/检查点可继续，但正式发布及成员/词义变更暂停。每个读取事务只读取一次 active_version，避免混版本。

新投影写入独立version；按当前有效修订集合及观察时间稳定回放，比较数量、SHA-256事实摘要和状态；原子切换active_version并解除闸门。失败/崩溃保留旧投影和维护模式。policy_only重建abort且事实修订摘要未变时可恢复旧投影；已写证据修订的重建abort必须保持维护、requires_rebuild=true，restart新run成功后才解闸。CLI resume/abort/restart及完整状态表见10 R03；不得因超时开放写入，目标version不能覆盖已成功结果。

首版题目/候选纯文本与 JSON 全在 app.db，不依赖外部媒体附件。备份用 Online Backup API 或驱动等价 `VACUUM INTO` 一致性快照；manifest 的 event_watermark 必须从快照读出。输出写新临时目录，校验后原子 rename 为完成目录。manifest v1：format_version、schema_version、app/dependency/policy/projection versions、SQLite version/source_id、snapshot_watermark、created_at、files[{relative_path,role,bytes,sha256}]。路径不得绝对或包含 `..`。以后引入附件需升级备份能力，并以内容哈希寻址/先落盘后引用保证快照引用闭包。

checkpoint 可不备份：业务库固定输入、候选、处置和租约才是审计来源。若包含，暂停 worker 并单独快照，两库不声称同一事务。恢复到全新目录后运行 integrity_check、foreign_key_check、清单哈希、事件/投影水位、实例/幂等/来源引用检查；所有未完成租约作废并递增 fence，按业务 item 对账 checkpoint 后恢复。未知新 schema/manifest 版本拒绝恢复；不覆盖活动库。实现完成前必须演练一次真实的新目录恢复及投影回放。

## 8. 实施依赖与完成门槛

| 顺序 | 可启动条件 | 完成证据 |
| --- | --- | --- |
| 冻结基线（本轮） | 1.1.0契约、策略矩阵、fixture、任务清单 | 依赖/契约检查及真实浏览器工具链探针；不表示业务E2E通过 |
| M00→M01 | 运行/事务/验收器→标准内容与题库 | 真实Go服务、SQLite、浏览器与固定种子 |
| M02 / M05 / M06 | M01就绪后可并行 | 学习引擎CLI组件E2E、导入页面、生成租约/预算协议 |
| M03 / M04 / M07 | 各自满足09依赖，可并行 | 训练/考试浏览器E2E、Go/Python/provider跨进程E2E |
| M08→M09 | 学习事实、考试和生成相关依赖完成 | 处置/rebuild/abort/restart及真实新目录恢复 |
| M10 / M11 | 分别满足09依赖与生产配置 | 完整个人浏览器流程；真实模型检查集门槛分别验收 |

ECDICT候选完整原始快照及许可已下载并校验，实际行数/标签统计见07；尚需实现词义解析与质检后才能成为正式内容。后续外部输入是生产模型profile/凭证与预算配置。算法效果、误检率及真实使用数据属于后续验证；这些不阻塞Go首版开发，也不能写成本轮已验证。
