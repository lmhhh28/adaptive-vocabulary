# 后端代码审查：稳定性与性能

日期：2026-09-10。基线：`19d9a56`，审查开始时工作区无未提交改动。

结论：确认 14 项问题，其中 3 项 P1、11 项 P2。优先处理活动投影版本、选题复杂度和生成工作流终态。所有问题均有实际调用链；本报告没有把未调用的辅助函数、符合设计的迟到对账、或已受全局中间件保护的入口列为缺陷。

P1 表示应优先修复的核心流程故障或明显性能瓶颈；P2 表示可触发的一致性、恢复或输入边界缺陷。问题排序不代表同级问题之间严格的实施顺序。

## 审查范围和方法

- Go：`cmd/avocab`，`internal/platform`、`core`、`content`、`bank`、`importer`、`practice`、`exam`、`learning`、`selection`、`generation`、`revision`、`maintenance`、`backup`，及对应迁移和测试。
- Python：`worker/avocab_worker`，包括配置、HTTP 客户端、租约、LangGraph 工作流、运行器与恢复入口。
- 参照数据库/HTTP 契约及重建、预算账本设计，按本机单人使用、SQLite WAL、业务库单连接的实际运行方式判断影响。
- 运行全量 Go 测试和竞态检查：`go test -race -count=1 ./...`，13 个有测试的包全部通过。
- 运行 `go vet ./...`，通过。
- 在独立临时数据库中运行定向复现，使用真实迁移、seed 和业务服务；HTTP 输入用真实 handler 验证；Python 失败路径用真实 LangGraph 图与替身 Go/provider 客户端验证，没有调用外部模型。
- 未运行整个浏览器 E2E 套件、真实付费供应商链路或生产历史数据压测。测试通过不能覆盖下面的状态转换和规模问题。

## 1. [P1] 重建切换活动版本后，选题与新作答仍读写 v1

位置：[practice/service.go:384](/Users/lmhhh/Desktop/adaptive-vocabulary/internal/practice/service.go:384)、[practice/handler.go:64](/Users/lmhhh/Desktop/adaptive-vocabulary/internal/practice/handler.go:64)、[exam/handler.go:43](/Users/lmhhh/Desktop/adaptive-vocabulary/internal/exam/handler.go:43)。切换发生在 [maintenance/rebuild.go:146](/Users/lmhhh/Desktop/adaptive-vocabulary/internal/maintenance/rebuild.go:146)。

`ExecuteReplay` 成功后将 `projection_control.active_version` 改为新版本，但 `NextQuestion` 显式传入 `ProjectionVersion: "v1"`；训练和考试的 LearningService 也始终以 `v1` 初始化，写入时没有读取活动版本。学习详情则读取 `active_version`，因此两个路径会使用不同状态。

触发：完成一次作答，结束会话，对该题执行 invalidate 并成功重建，再开始练习并完成新的作答。

复现：新活动版本的 `ordinary_samples` 在失效后为 0；新答题成功后，旧 `v1` 的样本数变为 2，新活动版本仍为 0。重建成功并未让后续学习使用新状态。重启服务同样会以硬编码 `v1` 初始化，不能消除问题。

影响：失效题的旧证据仍影响选题，重建后的学习详情停止反映新作答，FSRS 与展示状态持续分离。

建议：在作答事务内读取当前活动投影版本并用于所有投影写入；选题读取该版本，保留题目事务内校验版本未变化。不要只在服务启动时缓存版本。

## 2. [P1] 选题按全部词义和题型反复扫描题目表，5000 词义已接近请求超时

位置：[selection/selector.go:199](/Users/lmhhh/Desktop/adaptive-vocabulary/internal/selection/selector.go:199)、[selection/selector.go:417](/Users/lmhhh/Desktop/adaptive-vocabulary/internal/selection/selector.go:417)、[selection/cooling.go:31](/Users/lmhhh/Desktop/adaptive-vocabulary/internal/selection/cooling.go:31)。

每次选题遍历整个题库的词义与三种题型，并分别查询 published versions、cooldown、evidence、memory。`question_version` 没有与 `sense_id/task_family/status/created_at` 匹配的索引，因此即使某词义没有该题型，也会执行一次题目表扫描。只在 Go 中按 form 去重还会读出重复版本的完整 JSON。随着词义数与题目数同时增长，题目扫描部分接近 O(B×Q)，有历史后还会增加 cooldown 聚合成本。

本机合成数据测量：生产单连接/WAL 配置，每个新增词义只有一道 meaning_recognition 题，无作答历史；下面数量是在 seed 基础上新增的词义数，每个规模测量 3 次。

| 新增词义数 | 第 1 次 | 第 2 次 | 第 3 次 |
| --- | ---: | ---: | ---: |
| 100 | 11.16 ms | 11.06 ms | 11.25 ms |
| 1000 | 283.91 ms | 284.38 ms | 285.15 ms |
| 5000 | 14.63 s | 14.63 s | 14.68 s |

这些是本机服务函数计时，不是生产 P95，也没有包含 HTTP 开销。当前 HTTP `WriteTimeout` 为 15 秒（[platform/server.go:178](/Users/lmhhh/Desktop/adaptive-vocabulary/internal/platform/server.go:178)），5000 词义这一并不超出导入能力的规模已接近超时；更多题型、版本或历史可能进一步恶化。

建议：优先批量读取题库中有可用题目的 unit 及投影，只对最终少量候选加载题目正文；补齐题目查询和有效历史聚合所需索引。用相同规模基准验证修改，不仅检查小 seed 的选题正确性。

## 3. [P1] worker 在预算耗尽或供应商失败后跳过 completion，任务长期停留在处理中

位置：[worker/avocab_worker/graph.py:337](/Users/lmhhh/Desktop/adaptive-vocabulary/worker/avocab_worker/graph.py:337)、[graph.py:395](/Users/lmhhh/Desktop/adaptive-vocabulary/worker/avocab_worker/graph.py:395)。

`budget_exhausted`/`failed` 被路由到 `halt → END`，而 `complete_item` 自身又对这两个状态直接返回。Go 的 `ReserveCall` 在预算不足时只返回错误；把 item/job 置为终态并扣减 pending 的逻辑在 `CompleteItem` 中，因此图结束不等于业务任务结束。

复现：真实图执行中，预算耗尽得到 `status=budget_exhausted`、`complete_calls=0`；供应商失败得到 `status=failed`、`settle_calls=1`、`complete_calls=0`。

影响：item 留在 claimed，租约过期后可再次领取。预算已耗尽时再次执行仍会失败，job/pending 无法收敛；暂时性网络故障也要等待租约恢复。

建议：将仍持有有效租约的预算耗尽和确定失败转入 completion 节点，并整理未完成预留。已丢失租约的执行必须停下，由新 owner 处理，不能把所有失败一律强行 complete。

## 4. [P2] evaluator 的 base_url 和 API key 配置没有用于实际请求

位置：[worker/avocab_worker/runner.py:29](/Users/lmhhh/Desktop/adaptive-vocabulary/worker/avocab_worker/runner.py:29)、[graph.py:247](/Users/lmhhh/Desktop/adaptive-vocabulary/worker/avocab_worker/graph.py:247)。

Runner 只用 generator 配置创建一个 ProviderClient，评估节点复用它，仅切换 model/max_tokens。`WorkerConfig.evaluator_api_key` 虽然已解析，却没有进入该请求链。

触发：generator/evaluator 使用不同 endpoint 或凭证。构造复现中仅创建 1 个 client，其 URL 是 generator URL，未使用 evaluator key。

影响：评估发往错误服务/账户，出现认证、模型不存在、配额问题，或质量评估没有使用用户指定的服务。

建议：分别创建并关闭 generator/evaluator client，节点按角色选择；增加不同 URL、不同 key 的集成测试。

## 5. [P2] 自动发布忽略程序检查失败，允许多个正确选项

位置：[internal/generation/service.go:836](/Users/lmhhh/Desktop/adaptive-vocabulary/internal/generation/service.go:836)、[service.go:888](/Users/lmhhh/Desktop/adaptive-vocabulary/internal/generation/service.go:888)、[service.go:939](/Users/lmhhh/Desktop/adaptive-vocabulary/internal/generation/service.go:939)。

Go 只验证选项总数和至少有一个 `is_correct`；循环将最后一个 true 写入 `correctOptionID`。`RuleChecks.single_correct` 未参与 allPass，Python 也只记录程序检查结果，没有将其合并入最终 verdict。因此两个 evaluator 都返回 pass 时，不需要伪造认证或绕过正常 worker 就可能触发。

复现：四个选项中两个 `is_correct=true`，显式 `rule_checks.single_correct=false`，其余有效 reservation/semantic ratings 满足协议，`SubmitCandidate` 返回 `disposition=published`。

影响：评分键与选项 JSON 的正确性标记矛盾，练习评分、考试反馈和混淆投影可能基于不同答案含义更新。

建议：Go 发布入口独立强制恰好一个正确项、选项 ID 唯一、题干非空等基础结构规则；把 worker 程序检查失败纳入质量拒绝流程。

## 6. [P2] 复用 canonical option 时，新题保存了数据库中不存在的 identity ID

位置：[internal/generation/service.go:915](/Users/lmhhh/Desktop/adaptive-vocabulary/internal/generation/service.go:915) 至 `processedOpts` 构造段；正文引用保存于 [service.go:963](/Users/lmhhh/Desktop/adaptive-vocabulary/internal/generation/service.go:963)。

当候选未给出 `option_identity_id` 时生成新 UUID；插入遇到相同 canonical_key 后 `DO UPDATE SET id=id` 保留旧行，但之后仍把新 UUID 写入题目 options_json，没有查询真正被保留的 ID。

复现：两个不同生成 item 发布含相同语义选项、不同题干的题目，第二题的 4 个 option identity 引用全部无法在表中找到。

影响：相同干扰项的混淆记录被拆成不同或悬空身份，跨题形式的历史聚合失效。JSON 引用没有数据库外键兜底。

建议：以 canonical_key 插入/获取实际 identity ID，并把它写回持久化选项。内容服务也要统一该逻辑；[content/service.go:144](/Users/lmhhh/Desktop/adaptive-vocabulary/internal/content/service.go:144) 生成的 ID 甚至没有写回 `q.Options`。

## 7. [P2] invalidate 后回放仍把失效事件计入正常子组和混淆记录

位置：[internal/learning/service.go:369](/Users/lmhhh/Desktop/adaptive-vocabulary/internal/learning/service.go:369)、[service.go:490](/Users/lmhhh/Desktop/adaptive-vocabulary/internal/learning/service.go:490)，调用于 [learning/replay.go:156](/Users/lmhhh/Desktop/adaptive-vocabulary/internal/learning/replay.go:156)。

回放已识别 disputed/invalidate 并返回全 false 的 EligibilityDecision，但 updateEvidence 仍按 source/purpose 无条件累计 normal/early_review/correction/exam 子组；updateConfusion 则把未获普通资格的事件当作 uncertain 或 correction 继续写入。

复现：只有一次 normal 作答，invalidate 后新活动版本 `ordinary_samples=0`，却有 `normal_samples=1` 和 3 条 confusion projection。

影响：普通并集和子组分母无法对账，失效/争议数据混入正常或不确定观测组。FSRS 普通资格被关闭并不代表所有投影都已剔除该事件。回放没有调用 correction 更新，因此本报告不声称回放会重新创建 correction queue。

建议：明确有效、隔离、失效三种处置，把有效性独立于普通证据资格传递到各 projection updater；失效事件不应借 uncertain 组继续参与正常统计。

## 8. [P2] 显式 settle(unknown) 后，reservation 无法再接受一次确定对账

位置：[internal/generation/service.go:644](/Users/lmhhh/Desktop/adaptive-vocabulary/internal/generation/service.go:644)。

第一次 `SettleCall(outcome=unknown)` 就保存 settlement_digest，之后凡摘要不同均报冲突；没有区分 unknown 中间状态与 settled 最终状态。

复现：同一个 reservation 先 unknown，再提交 response 和确定 usage，返回 `CALL_SETTLEMENT_CONFLICT`。这与 [设计中的 unknown→settled 和迟到对账规则](/Users/lmhhh/Desktop/adaptive-vocabulary/docs/10-review-resolutions.md:43) 不符。

影响：用户得到确定用量后仍不能把保守预留替换成实际值，预算可能一直被高估占用。

建议：只对最终 settled 固定最终摘要；unknown 允许一次原 owner/fence 身份的确定结算，按状态条件更新和调整账本，仍拒绝改写已 settled 的记录。由过期恢复自动标记 unknown 且没有摘要的路径与本问题不同。

## 9. [P2] 非法或缺失 abandon 请求体会直接关闭 revision=0 的考试

位置：[internal/exam/handler.go:439](/Users/lmhhh/Desktop/adaptive-vocabulary/internal/exam/handler.go:439)。

handler 忽略 ReadAll 和 Unmarshal 错误，`ExpectedRevision` 的零值无法区别字段缺失与合法 revision=0。

复现：新建考试后分别提交空 body、`{`、`{}`，三个请求均返回 HTTP 200，session 变为 abandoned。

影响：格式错误或缺少前置条件的请求被执行成不可逆的考试放弃，而不是拒绝输入。handler 忽略读取错误也会掩盖全局体积限制返回的错误。

建议：检查读取和 JSON 解码错误，并用指针/显式字段存在性检查强制 expected_revision；真正传入 0 的请求应仍被允许。

## 10. [P2] telemetry-gap 的成功回执在考试关闭后不能重放

位置：[internal/exam/service.go:728](/Users/lmhhh/Desktop/adaptive-vocabulary/internal/exam/service.go:728) 至幂等检查段。

函数先检查维护与 session=open，之后才读取幂等记录。已提交 gap 丢失响应后，若考试提交/放弃/自动结算，再发同键同正文就得到 SESSION_CLOSED；成功回执实际上仍在数据库。

复现：成功 gap → 放弃考试 → 用同一 key/digest 重试，得到 `exam session is closed`。

影响：网络恢复流程把已成功动作报告成失败，无法按原回执确认水位。

建议：与 response-event/submit 等路径一致，先读取并校验已提交回执，再对新操作做维护、状态和 CAS 检查。

## 11. [P2] bank_sense 插入失败被吞掉，导入仍报告 completed/resolved

位置：[internal/importer/service.go:428](/Users/lmhhh/Desktop/adaptive-vocabulary/internal/importer/service.go:428)；手动 resolve 的 [service.go:731](/Users/lmhhh/Desktop/adaptive-vocabulary/internal/importer/service.go:731) 有相同模式。

`tc.Exec` 出错时仅跳过 RowsAffected 累计，不把错误向外返回，事务后续继续写 completed job。

复现：在临时数据库对 bank_sense 插入添加 `RAISE(ABORT)` 触发器以模拟该条语句失败，业务方法返回成功，job=completed、resolved=1，但 actual_members=0。该注入验证的是错误传播；没有声称所有真实磁盘故障都能在同一事务内继续提交。

影响：接口、审计记录和实际成员不一致，重试还可能命中已经记录的成功回执。

建议：除 `INSERT OR IGNORE` 已明确处理的重复外，遇到 Exec 错误立即返回，回滚整个导入；对 RowsAffected 错误同样处理。

## 12. [P2] 手动 resolve 缺少状态迁移限制，excluded 与实际成员不一致

位置：[internal/importer/service.go:706](/Users/lmhhh/Desktop/adaptive-vocabulary/internal/importer/service.go:706)、[service.go:750](/Users/lmhhh/Desktop/adaptive-vocabulary/internal/importer/service.go:750)。

代码只校验 revision，没有限制当前 status。resolved 项可转 excluded，已采用成员不会被移除，旧 resolved IDs 也继续保留。

复现：导入 river 自动 resolved，再调用 exclude，item.status=excluded、题库 member_count 仍为 1。

影响：消歧处置、导入统计与已采用结果互相矛盾。若之后重新 adopt 不同 sense，还会积累先前采用但结果中已不再列出的成员。

建议：将普通消歧限制在允许的初始状态；如支持重新处置，应设计明确的 reopen/撤销语义，记录成员来源，避免错误删除由其他导入或人工加入的成员。

## 13. [P2] 同一 sense 有多个版本时，题库详情返回重复成员

位置：[internal/bank/bank.go:181](/Users/lmhhh/Desktop/adaptive-vocabulary/internal/bank/bank.go:181)。

成员查询直接 JOIN sense_version，未选最新或指定版本，一个 bank_sense 会展开为多行。

复现：仅有一个成员的题库，为其新增 version_number=2 的 sense_version 后，返回 member_count=1、len(members)=2，并包含旧义项措辞。

影响：详情数量与成员列表不一致，多次修订会持续放大响应和 UI 重复项。

建议：明确成员展示的版本规则，在 SQL 中每个 sense 只选择一行最新/采用版本；若要展示版本历史，应作为成员内部的独立结构。

## 14. [P2] 检查点快照失败后退回裸文件复制，备份仍可能报告成功

位置：[internal/backup/service.go:140](/Users/lmhhh/Desktop/adaptive-vocabulary/internal/backup/service.go:140)。

`--include-checkpoints` 路径的 VACUUM INTO 失败后直接复制主数据库文件，复制错误被忽略，后续只要能计算文件哈希就将其列入 manifest。哈希正确不代表文件是可用的一致 SQLite 快照。

复现：提供损坏的 langgraph-checkpoints.db，VACUUM 失败后 CreateBackup 仍返回成功，manifest 包含 app.db 与该损坏检查点。后续 Verify 可以发现这一损坏，但 CreateBackup 已经报成功。

WAL 场景下裸拷贝还可能漏掉已提交但未 checkpoint 的内容；某些旧主文件仍然能够通过 integrity_check，因此不能把恢复语义交给事后结构检查。

建议：请求包含检查点且快照失败时明确失败；若允许部分备份，则显式声明 omitted/error，而不是静默生成包含无效检查点的成功备份。采用可靠的 SQLite 快照方式，禁止对活动 WAL 主文件做该 fallback。

## 已排除或未提升为正式问题的观察

- **来源 JSON 导入并非无限请求体**：server 已统一应用 2 MiB MaxBytesMiddleware；缺少 handler 局部上限不能单独证明无限内存使用。
- **过期考试查询不是必然单连接死锁**：代码遍历 rows 到 EOF 后再起事务，database/sql 会自动释放结果集连接；现有单连接测试也通过。
- **旧 fence 的迟到 settle 是明确设计**：允许匹配原 reservation 身份的对账，候选发布才需要当前 lease。不能建议把所有迟到对账一律拒绝。
- **无证据判定崩溃恢复重发为预算漏洞**：设计要求每次实际新 HTTP 发送使用新预留，无法复用尚未持久化的模型响应时重新扣授权有其保守语义。
- **StartRebuildRun 并发保护不足未作为当前生产 finding**：该入口当前只有测试调用；HTTP evidence disposition 有事务内维护门检查，写库 CLI 共用进程锁。不能仅凭底层方法可被任意调用，就声称当前 API 已存在双执行者竞争。
- **曝光全表辅助查询未单独报告**：DeriveExposureLabels 当前没有生产调用；本报告性能结论来自真实 SelectNextQuestion 热路径。
- **Server.Close/Start 的等待行为属于生命周期扩展缺口**：CLI 目前通过信号控制，未找到当前业务因该组合产生永久阻塞的调用链。
- 未把恢复输入被外部并发替换、符号链接归档和断电持久化等仅静态推测的问题混入已复现问题计数。恢复的临时目录/失败清理仍值得后续专项演练。

## 验证文件

本目录保留 [Go 定向复现源文件](review_test.go.txt)，它是审查探针，不是正式回归测试：断言当前缺陷确实出现，故 PASS 表示复现成功，不表示行为正确。要重跑，临时放到仓库 `internal/reviewcheck/review_test.go`，保持工作目录和项目工具链设置，与 Makefile 一致。`TestReviewSelectionScaling` 在当前实现下约需 45 秒，其余用例很快。复现探针已移出业务目录，保存为 `.txt` 防止被 Go 自动发现，未修改生产代码、迁移或正式测试。
