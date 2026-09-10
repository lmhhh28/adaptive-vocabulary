后端完整代码审查：稳定性与性能

审查日期：2026-09-10。范围为当前工作区完整后端，包括 `cmd/avocab`、`internal` 下 13 个包、SQL migrations、Python `worker/avocab_worker` 及相关单元/E2E 测试。以项目的 localhost、单人使用边界评估严重程度；没有把多租户部署或互联网高并发作为默认前提。

确认 11 项问题：1 项 P1、10 项 P2。P1 表示应优先修复的进程可用性风险；P2 表示有具体触发条件的稳定性、数据一致性或性能缺陷。业务代码和已有测试未修改，新增文件仅为审查报告、隔离验证脚本和验证产物。

**F01 · P1 · 组卷题数上限未执行，可由很小的请求触发巨量内存分配。**

位置：[internal/exam/service.go:101](/Users/lmhhh/Desktop/adaptive-vocabulary/internal/exam/service.go:101)、[internal/exam/assembler.go:238](/Users/lmhhh/Desktop/adaptive-vocabulary/internal/exam/assembler.go:238)。

`question_count` 只验证大于零，未读取策略文件已经定义的 `exam.max_questions=200`。组卷器随后按每一题展开 `requiredTasks`，发生在候选不足判断、回溯节点上限和回溯中的 context 检查之前。因此全局 2 MiB 请求体限制并不能保护这里：只需传一个很大的整数即可制造大分配。该过程还处于 `RunInTx` 的写事务内。

验证：空题池、回溯上限设为 1、请求 1,000,000 题，仍累计分配 88,039,088 字节，之后才返回无可行试卷。这里测的是累计分配量，不是 RSS 峰值。更大的题数会继续增长，可能引发 OOM、影响整个服务；未执行会耗尽机器内存的极端请求。

建议：在进入事务和展开配额前强制执行配置中的最大题数；按题型和不同目标词数量快速判断可行性；将实际搜索限制也接入策略配置。当前 handler 的搜索上限硬编码为 20,000，策略文件为 10,000。

**F02 · P2 · 下一题的冷却检查按词重复扫描历史，长期使用后成为明显延迟来源。**

位置：[internal/selection/selector.go:313](/Users/lmhhh/Desktop/adaptive-vocabulary/internal/selection/selector.go:313)、[internal/selection/cooling.go:30](/Users/lmhhh/Desktop/adaptive-vocabulary/internal/selection/cooling.go:30)。

每个有已发布题目的不同 lexeme 都会查询最后预留，再对其后的作答记录执行 `COUNT(DISTINCT ...)`。业务只需要知道是否达到 5 个其他词，但查询会统计整个后续历史；即使已满足“新会话且超过 24 小时”的直接释放条件，也要先完成这次扫描。按词缓存仅避免同一次请求中同词多题型重复计算，没有消除逐词扫描。

验证：实际 Go SQLite 驱动、500 个曾预留的词条、50,000 条之后的练习作答，执行生产 `CheckNormalCooldown` 的一轮冷却检查耗时 10.880 秒。合成数据使用内存数据库，且只有 5 个不同的后续作答词；这是特定历史分布下的函数耗时，不是线上整个 `/next` 的 SLA 测量。

建议：先判断可直接释放的时间条件；对 distinct 计数使用“达到阈值即可停止”的查询，或一次批量计算候选词的冷却状态，避免每词重复遍历历史。保留最终写事务内的状态复核。

**F03 · P2 · 重建忽略启动时选择的 FSRS 策略，悄悄改变已有学习状态。**

位置：[internal/maintenance/rebuild.go:100](/Users/lmhhh/Desktop/adaptive-vocabulary/internal/maintenance/rebuild.go:100)、[internal/maintenance/rebuild.go:115](/Users/lmhhh/Desktop/adaptive-vocabulary/internal/maintenance/rebuild.go:115)。

在线 practice/exam 使用 `--policy` 指定的文件；重建却重新从几个相对路径查找固定的 `config/policy-v2.json`，不使用启动时解析的策略，也不按 run 的目标策略解析。找不到文件时还静默使用零值配置。自定义策略部署，或从仓库外的工作目录运行 CLI，都会造成在线学习和重建使用不同调度参数。

验证：生产二进制使用学习步长 `[1,60]` 的自定义策略完成一次作答，然后对未参与该次作答的另一道题执行证据处置以触发重建。目标词的 `due_at-last_review_at` 从 3,600,000,000 微秒变成 600,000,000 微秒，即 60 分钟变成 10 分钟，重建报告成功。

建议：把已经验证的策略对象或明确的不可变策略版本注入维护服务；run 持久化可恢复的策略身份；CLI resume/restart 同样使用显式策略配置，加载失败应阻止重建发布。

**F04 · P2 · 导入忽略幂等记录写入失败，业务提交与重试保护不再原子。**

位置：[internal/importer/service.go:492](/Users/lmhhh/Desktop/adaptive-vocabulary/internal/importer/service.go:492)。同类调用还在 source import 和 resolve 路径中。

`SaveIdempotencyRecord` 的错误被 `_ =` 丢弃，事务回调仍返回 nil。SQLite 的语句失败不一定自动回滚整个事务，因此业务导入可以提交，而相同请求的重试找不到收据，会重新执行。

验证：仅在隔离数据库注入使 `idempotency_record` INSERT 执行 `RAISE(ABORT)` 的触发器。同一个 key、同一个导入请求返回两次 HTTP 200、两个不同 import ID；数据库保留 2 个 import job、0 条对应的幂等记录。此验证证明错误处理缺口，不声称当前生产库存在该触发器。

建议：所有幂等收据保存失败都向上传播，回滚整个业务事务；同时检查 source import、bank import 和 resolve 三条路径。

**F05 · P2 · 模型输出只验证 JSON 语法，错误类型导致任务跳过正常完成流程。**

位置：[worker/avocab_worker/provider_client.py:165](/Users/lmhhh/Desktop/adaptive-vocabulary/worker/avocab_worker/provider_client.py:165)、[worker/avocab_worker/graph.py:222](/Users/lmhhh/Desktop/adaptive-vocabulary/worker/avocab_worker/graph.py:222)。

解析器允许 `null`、数组、标量等合法 JSON 通过，调用者却直接按 dict 使用。生成器在成功结算调用之后执行 `"task_family" not in candidate_content`；若内容为 `null`，会抛出 TypeError，绕过 `complete_item`。runner 捕获异常后继续轮询，该 item 留在 claimed 状态，等待租约过期后重领并再次调用模型，不能及时形成清晰的内容失败状态。

验证：使用真实 provider parser 和 LangGraph，HTTP MockTransport 返回 200、合法外层响应、`message.content="null"`。结果为 `argument of type 'NoneType' is not iterable`，结算次数 1、complete 次数 0；没有调用外部模型。嵌套 options/ratings 的类型也应使用同一边界校验。

建议：对模型内容做对象类型及任务结构验证，保留外层 usage/response 审计信息，把内容格式错误转为明确的可控失败或修订状态，并保证合法租约下的任务最终完成。

**F06 · P2 · 临时 503 或超时直接永久失败，没有执行约定的网络重试。**

位置：[worker/avocab_worker/graph.py:187](/Users/lmhhh/Desktop/adaptive-vocabulary/worker/avocab_worker/graph.py:187)、[worker/avocab_worker/graph.py:299](/Users/lmhhh/Desktop/adaptive-vocabulary/worker/avocab_worker/graph.py:299)。

冻结设计约定每个逻辑调用最多 3 次尝试及 1/2 秒退避，当前策略也包含 `max_network_attempts_per_call=3`；实现却把第一次 ProviderError 直接转成 failed，并调用 complete 将 item 终结。Claim 只选 pending 或过期 claimed，因此该失败不会自动被正常轮询重新领取。短时供应商故障会造成永久失败，并浪费已经完成的生成或评估阶段。

验证：mock provider 返回一次 503，graph 的 provider 调用次数为 1，最终 status 和 complete outcome 都为 failed。现有 `test_provider_error_on_call_generator` 也只验证了失败收敛，没有验证允许的网络重试。

建议：区分可重试的网络故障、429/5xx 与永久错误；为每次实际重试分配独立 reservation 和 network_attempt_id，在总预算、逻辑尝试上限和租约范围内有界退避。

**F07 · P2 · max_concurrent_items 限制的是单次领取数量，无法限制实际在途任务数。**

位置：[internal/generation/service.go:281](/Users/lmhhh/Desktop/adaptive-vocabulary/internal/generation/service.go:281)、[internal/generation/service.go:293](/Users/lmhhh/Desktop/adaptive-vocabulary/internal/generation/service.go:293)。

代码仅把请求 limit 截断到配置值，没有在同一事务中统计未过期的 claimed 数量。多个 worker 或同一 worker 重复 claim 都能不断取得新租约。并发配置因此不能约束实际模型调用、数据库负载和供应商限流压力。

验证：生产二进制、配置上限为 2，一个含 3 个 item 的 job，两个不同 worker 依次 claim(limit=2)，分别领取 2 个和 1 个；3 个 item 的租约同时有效，无需真正并发请求即可突破限制。

建议：先明确上限作用域为全局或每 worker，再在领取事务中计算有效在途数及剩余容量，并将统计与分配保持原子；过期租约不得占用有效容量。

**F08 · P2 · 导入确认可以在活动学习会话中改变题库成员。**

位置：[internal/importer/service.go:734](/Users/lmhhh/Desktop/adaptive-vocabulary/internal/importer/service.go:734)。

普通题库删除/归档明确检查活动 session；resolve adopt 只检查维护状态、导入 item 版本和 sense 存在性，就插入 bank_sense 并推进 bank revision，未执行 session 限制。整个 importer 也没有活动会话检查。题库可以在用户会话进行中变化，使冻结成员、修订和后续选题的一致性约束失效。resolve 还未复核题库归档状态，建议同一修复中统一处理。

验证：创建题库并生成待确认的 not_found item，在该题库启动 practice session，再 adopt 一个有效 sense，返回 HTTP 200；数据库仍有 open session，题库成员数已经增加到 1。这个复现不依赖并发或故障注入。

建议：在同一事务内复用统一的题库可修改状态校验，覆盖 bank import 和 resolve；保持已有幂等请求的重放规则。

**F09 · P2 · 过期考试结算失败被吞掉，启动仍报告 ready。**

位置：[internal/exam/service.go:1367](/Users/lmhhh/Desktop/adaptive-vocabulary/internal/exam/service.go:1367)，调用者为 [internal/platform/server.go:91](/Users/lmhhh/Desktop/adaptive-vocabulary/internal/platform/server.go:91)。

`SettleExpiredExams` 仅在事务成功时增加 settled，失败后继续，最终始终返回 nil error。启动逻辑本意是在恢复完成后就绪，实际上无法感知任何单次结算失败。由于数据库只允许一个 open session，未结算的过期考试还会占用全局会话位置，影响新练习或考试。

验证：隔离生产实例创建定时考试、设置过期，并在 assessment_result INSERT 注入语句失败。重启仍成功就绪、health 返回 HTTP 200，考试 session 保持 open。故障注入仅存在于临时测试库。

建议：传播带 exam ID 的结算错误，让启动拒绝 ready，或实现显式且可观察的受限运行状态；不能把未恢复成功作为正常启动。

**F10 · P2 · event_revision 缺少查询索引，每次作答和每条重建事件都全表扫描。**

位置：[internal/learning/replay.go:145](/Users/lmhhh/Desktop/adaptive-vocabulary/internal/learning/replay.go:145)、[migrations/020_learning.sql:95](/Users/lmhhh/Desktop/adaptive-vocabulary/migrations/020_learning.sql:95)。在线路径同样位于 [internal/learning/service.go:65](/Users/lmhhh/Desktop/adaptive-vocabulary/internal/learning/service.go:65)。

查询按两个 event_id 找最新修订，并按 created_at/id 排序，但表上只有 id 主键。实际查询计划为 `SCAN event_revision; USE TEMP B-TREE FOR ORDER BY`。重建对每个 attempt 执行该查询，修订数 R 和作答数 A 增长时会出现近似 A×R 的扫描成本，同时延长维护事务。

验证：当前 Go SQLite 驱动、100,000 条 revision、100 次未命中查询，原结构耗时 669.360 毫秒；只在内存测试库加入 `(event_id, created_at DESC, id DESC)` 索引后，同组查询耗时 0.933459 毫秒。这是本次微基准，不是任意数据分布的固定加速保证。

建议：用新增 migration 添加适配该查询的索引；重建可进一步批量加载最新 revision，消除逐事件查询。对实际 OR 查询检查执行计划，避免添加无效或重复索引。

**F11 · P2 · 恢复直接写最终目录，失败后留下半成品并阻止同目录重试。**

位置：[internal/backup/restore.go:71](/Users/lmhhh/Desktop/adaptive-vocabulary/internal/backup/restore.go:71)、[internal/backup/restore.go:179](/Users/lmhhh/Desktop/adaptive-vocabulary/internal/backup/restore.go:179)。

恢复通过源备份验证后直接把 app.db 等文件放入最终目标目录，再执行租约协调与数据库检查。之后任何错误或进程中断都可能留下未完成的恢复目录；下次调用又因目标非空而立即拒绝。备份创建已有临时目录与 rename，恢复端没有相应的提交/清理机制。这里没有发现覆盖原有数据库的行为，问题是失败结果和可重试性。

验证：在隔离的合法源库加入 generation_item UPDATE 故障触发器，生成并验证备份后执行恢复。第一次在 reconciliation 返回错误，但目标 app.db 已存在；第二次返回 `restore target must be an empty new directory`。

建议：在同一父目录使用 staging 目录，完成复制、协调、完整性校验后提交；失败清理本次 staging。若要保留中间产物，应提供明确的恢复未完成标志和受控续恢复机制，不要要求用户猜测哪些文件可删除。

**验证与覆盖说明**

| 检查 | 结果 |
| --- | --- |
| `make test` | Go 各包通过；Python unittest 6 个通过 |
| `go test -race -count=1 ./...`（项目内置 Go） | 全部通过，无检测到的数据竞争 |
| `go vet ./...`（项目内置 Go） | 通过 |
| `make e2e MODULE=all` | 45/45 用例文件通过，0 失败、0 跳过 |
| 生产二进制专项 HTTP/故障注入 | 5/5 复现探针通过 |
| SQLite 查询计划与微基准 | 3 个探针完成，确认分配放大、冷却扫描和 revision 查询成本 |
| 备份恢复错误路径 | 1 个探针确认半成品阻止重试 |
| Python parser/graph 本地 mock | 确认异常类型不收敛、首次 503 永久失败 |

专项探针的断言刻意确认“当前缺陷可以复现”；它们通过不代表缺陷已修复。所有写入均位于临时或审查测试数据库，未触碰 `data/local`。没有调用付费模型，没有执行破坏性极端内存请求，也没有进行真实磁盘满/断电测试。

必要的调用链复核排除了以下初筛项：上传无请求体限制（已有全局 2 MiB middleware）；崩溃重领复用同一付费 reservation（正常 runner 使用新 attempt/fence 和新预留，现有 WK-02 也验证账本）；可从公开路径随意并发覆盖 rebuild run（现有维护门闩与 CLI 进程锁限制了入口）；证据处置后无限期继续使用旧投影（处置事务同步设置维护状态）。这些未列为缺陷。

另有可优化但未提升为独立缺陷的项目：claim 的历史表扫描/排序、form exposure 统计查询、全量历史重建与报表的峰值内存、部分配置项未使用和多处被忽略的读错误。当前没有足够的实际影响验证，或与上述已确认问题重叠；不应把它们全部作为同等严重度的问题追踪。

复现及结果：

- [生产 HTTP 验证脚本](/Users/lmhhh/Desktop/adaptive-vocabulary/artifacts/reviews/2026-09-10/full-review-validation.py)
- [生产 HTTP 验证结果](/Users/lmhhh/Desktop/adaptive-vocabulary/artifacts/reviews/2026-09-10/full-review-probes-9cffa0f7/result.json)
- [SQL/分配探针源码](/Users/lmhhh/Desktop/adaptive-vocabulary/artifacts/reviews/2026-09-10/full-review-sql_test.go.txt)
- [恢复错误路径探针源码](/Users/lmhhh/Desktop/adaptive-vocabulary/artifacts/reviews/2026-09-10/full-review-backup_test.go.txt)
- [Go overlay 配置](/Users/lmhhh/Desktop/adaptive-vocabulary/artifacts/reviews/2026-09-10/full-review-overlay.json)
- [Worker 验证脚本](/Users/lmhhh/Desktop/adaptive-vocabulary/artifacts/reviews/2026-09-10/full-review-worker-probe.py)
- [Worker JSON 类型异常结果](/Users/lmhhh/Desktop/adaptive-vocabulary/artifacts/reviews/2026-09-10/full-review-worker-result.json)
- [Worker 重试结果](/Users/lmhhh/Desktop/adaptive-vocabulary/artifacts/reviews/2026-09-10/full-review-worker-retry-result.json)
- [完整 E2E 结果](/Users/lmhhh/Desktop/adaptive-vocabulary/artifacts/e2e/run_20260910_112521_6b455d6f/all/result.json)

复现命令（在仓库根目录执行，不修改产品文件）：

```sh
.tools/uv/bin/uv run --project e2e --locked python artifacts/reviews/2026-09-10/full-review-validation.py
.tools/uv/bin/uv run --project worker --locked python artifacts/reviews/2026-09-10/full-review-worker-probe.py
GOPATH="$PWD/.cache/go" GOCACHE="$PWD/.cache/go-build" GOTOOLCHAIN=local .tools/go/bin/go test -count=1 -v -overlay artifacts/reviews/2026-09-10/full-review-overlay.json ./cmd/avocab ./internal/backup -run '^TestReview' -timeout 90s
```

修复顺序建议：先处理组卷资源上限；接着修复重建策略身份、导入事务错误传播和启动恢复错误传播；随后解决冷却热点及 revision 索引；再完善 worker 重试、结构校验、实际并发上限和恢复 staging。每项修复应针对这里的触发条件补充回归测试。
