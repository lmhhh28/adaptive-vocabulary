# 架构与数据模型

本文是逻辑模型和一致性约束，不是最终 SQL schema。关系字段可标准化，题目与生成原始内容可用 JSON 快照；实现前据实际查询补齐索引。

## 1. 运行结构

```text
浏览器
  └─ Go HTTP 服务
       ├─ vocabulary / bank / content
       ├─ practice / exam / grading
       ├─ evidence / memory / confusion / selection
       ├─ generation jobs / quality issues
       └─ app.db

Python LangGraph worker
  ├─ 经 Go 内部接口领取作业、续租、提交候选与质检结果
  └─ langgraph-checkpoints.db
```

Go 是正式业务数据的唯一写入入口。Python 管理自身工作流检查点，不能直接修改题库、评分或学习状态。两者先运行在同一主机；首版不引入 Redis、消息队列服务或微服务拆分。

生成可以与作答并行。LLM 调用在数据库事务外执行，不因模型等待持有业务写锁。

## 2. 内容身份与事实实体

| 实体 | 关键字段与约束 |
| --- | --- |
| `lexeme` | id、canonical_form；屈折词形按明确规则归一，派生词不自动合并；考试按该 ID 去重 |
| `sense` / `sense_version` | 稳定 sense_id、lexeme_id、词性、释义、来源/版本；措辞修订新建版本，词义范围改变新建身份 |
| `source_entry` | source_id、external_entry_id、source_version、词条/词义映射、可用范围说明、词频/重要度来源 |
| `bank` / `bank_sense` | 题库与目标词义成员关系；保存采用的导入解析结果 |
| `import_item` | raw_input、规范化结果、候选匹配、resolved_sense_ids、解析规则版本、状态及原因 |
| `generation_job` / `generation_item` | 固定输入引用、配置、逻辑任务 ID、租约、尝试编号、进度与异常 |
| `question_candidate` | 候选正文、输入引用/摘要、模型和 prompt 版本、程序/语义检查、修订祖先、最终处置 |
| `question_form` | 内容形式的稳定标识；根据规范化题干、目标词义、task_family、语义选项集合识别 |
| `question_version` | form_id、sense_version_id、task_family、四个选项及 ID、答案键、解析、candidate_id、发布/停用信息 |
| `question_instance` | 版本引用及不可变完整快照、选项顺序、答案键、评分规则、session_id、序号 |
| `quality_finding` | 被检查对象、问题类型、依据、检查器版本、自动/人工处置及关联修订 |

question_form 表示题目内容形式；改标点或修正文案可以产生新版本，但不自动算作新的练习形式。form ID 不证明难度等价，仍保留完整实例供分析。

形式身份的最小判定规则：

| 变化 | form_id | 版本/处置 |
| --- | --- | --- |
| 选项顺序变化 | 保持 | 仅生成新 instance 的展示顺序 |
| 标点、排版、拼写修正或不改变语义的措辞修订 | 保持 | 正文改变时创建新 version |
| 仅纠正原来标错的答案键，展示内容和语义选项集合未变 | 保持 | 新 version，旧版进入错误处置；不能借此充当纠错新形式 |
| 替换一个干扰项的语义身份 | 新建 | 完整选项组合重新质检 |
| 改变目标词义、task_family、正确答案的语义内容或题干的语义条件 | 新建 | 新 version，必要时同时建立新 sense 身份 |
| 来源引用修订，展示内容及目标语义均未变 | 保持 | 来源/内容版本留痕 |

不能自动判定措辞是否等义时进入内容异常处置，不依赖文本哈希宣称已经识别语义等价。规范化哈希仅用于明确的重复检测。

题目版本正文不可原地改写。发布/停用状态通过独立状态字段及变更记录管理。题库移除成员或停用不级联删除已发生的作答和全局学习状态。

## 3. 会话与行为事实

| 实体 | 用途 |
| --- | --- |
| `practice_session` | 当前题库、状态、游标、已保留实例及纠错进度 |
| `exam` | 组卷条件、固定实例集合、状态、可选限时、交卷版本 |
| `response_event` | 考试首次选择、改选、取消选择等追加事件；客户端事件 ID、服务器序号与接收时间 |
| `exam_answer_draft` | 可重建的当前答案视图，指向对应 response_event |
| `assessment_result` | 最终答案快照、逐题与总分、评分版本、评分时间；是评分事实，不等同于记忆观测 |
| `attempt_event` | 训练单次提交，或已交卷考试每题的观测容器；关联选择时间线及最终评分 |
| `exposure_event` | 发题、实际展示遥测、反馈展示、相关词出现；记录事件来源和可信度 |
| `event_revision` | 判分纠正、证据失效等追加处置；不修改原始作答事实 |

考试改选必须作为有序事件发送和落库；仅周期性保存最终选项不足以恢复首次选择。缺少可靠选择时间线时标记 telemetry_incomplete，不能假定未改答。

建议 attempt 保存或引用：instance_id、sense_id、task_family、source、purpose、selected/final option、first_response_event_id、final_response_event_id、revision_count、server timestamps、client_active_duration、interruption_flags、grading_version、事件序号。未作答用 omitted 独立表示。

服务器收答时间是业务排序依据，但不等于认知发生的精确时间。客户端有效停留、实际展示和中断只作带来源的遥测；发题成功不证明用户已看见。

## 4. 从事实派生的投影

| 投影 | 主键与内容 | 边界 |
| --- | --- | --- |
| `evidence_projection` | sense_id + task_family；各观测分组样本数、正确数、跨日期/形式覆盖、近期表现、有效 RT 样本 | 描述性证据，不是校准能力概率 |
| `memory_projection` | sense_id + task_family；FSRS state/D/S、last_review_at、due_at、reps、lapses | 时间调度基线；可从事实与策略重建 |
| `confusion_projection` | target_sense + task_family + distractor_identity；出现机会、错选、最近时间及观测分组 | 避免只有错选次数而没有分母 |
| `question_projection` | question_form/version；作答、正确、耗时、暴露/目的分组 | 个人样本统计，不直接称题目难度 |
| `correction_item` | 当前会话与 unit、触发事件、已穿插目标词、重试数、状态 | 业务队列，不是第二个记忆模型 |

识别题的中文干扰释义不一定唯一对应一个英文单词。distractor_identity 应引用实际选项内容和可确定的标准词义，不能强制每个中文选项都有唯一 distractor_lexeme_id。

投影可先由 SQL 查询实现；高频选题需要时才物化。materialized projection 是持久化的派生状态，不是可以任意过期、丢失后继续错误使用的缓存。

投影记录 projection_version、observation_policy_version、applied_event_sequence、revision。选择策略也固定 selection_policy_version，保存候选分组和 selection_reason。

## 5. 版本、顺序与重建

业务接收序号全局单调递增；考试观测在交卷后才对模型生效，但其观察时间引用选择过程中的服务器时间。单活动会话约束避免考试未交卷期间插入另一轮练习更新。相同时间采用稳定事件 ID/序号打破并列。

FSRS 元数据分别保存：scheduler_family、algorithm_version、implementation、implementation_version、实际权重/parameter_set、desired_retention、调度选项、observational policy。Go 模块 /v4 与 FSRS-6 不是同一个版本号。[S02](06-sources.md)

如启用随机选题或 interval fuzz，保存随机输入/种子及相应算法版本；单有种子并不能跨随机算法版本保证重现。已展示题目由实例快照保证历史准确性。

重建流程：固定事件水位 → 在新投影版本回放 → 校验事件数量/摘要及关键状态 → 原子切换活动版本。首版重建期间暂停新作答写入；生成服务可继续处理不影响水位的任务。重建失败保留原活动投影，不能先清空正在使用的状态。

model_rebuild_run 保存起止版本、水位、事件数、结果摘要、完成状态和错误。正常事务写入保证事实与投影一致；崩溃恢复不得跳过尚未应用的事件。

## 6. 事务与幂等

训练提交事务：校验会话/实例 → 检查幂等 → 按快照评分 → 追加事实 → 更新相应投影与纠错队列 → 推进游标。没有有效选项的请求不形成知识错误。

下一题在短事务之外计算，再通过 `(session_id, cursor)` 唯一约束保留一个实例；重复请求返回同一实例。对同一实例的训练重复提交只返回既有反馈，不再次更新学习状态。

考试交卷事务：条件转换 open → submitted → 冻结草稿/时间线引用 → 评分 → 生成每题正式观测 → 按规则应用投影 → 保存报告。交卷与最后一次草稿写入用会话 revision 防止竞争；服务端设题量上限。

幂等键相同且载荷相同返回原结果；相同键不同载荷报冲突。实例唯一约束、考试终态约束防止换一个键重复计入。生成任务领取有租约；租约持有者与逻辑 item identity 一并校验，过期 worker 的结果不得重复发布。

## 7. SQLite 运行约束

- app.db 和 langgraph-checkpoints.db 物理分离；两种运行库都检查各自实际版本。
- WAL 场景使用已修复 WAL-reset 缺陷的运行库。新实现建议 SQLite >= 3.51.3；官方回移修复版本 3.44.6、3.50.7 也已修复，只有选用旧分支时才考虑。[S07](06-sources.md)
- 检查 Go 实际连接报告的 sqlite_version/source_id，而不是用系统 sqlite3 CLI 的版本代替。Python 的运行库也单独核对。
- 每个业务连接启用 foreign_keys，配置 busy timeout；检查 journal_mode 设置实际返回 WAL。[S08](06-sources.md)
- WAL 允许读写并行，但每个数据库仍只有一个并发写者。事务短小，不在事务中等待模型或外部网络。
- 个人学习历史优先持久性，建议明确使用 synchronous=FULL；若以后选择其他模式，应记录耐久性取舍。
- 数据文件放在本机文件系统，不把活动 WAL 数据库作为网络共享文件使用。备份采用一致性快照，详见验证文档。
