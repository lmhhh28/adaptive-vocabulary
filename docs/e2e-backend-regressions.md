# 后端真实进程回归验收

本次测试在 2026-09-09 的已有 M00–M11 验收基础上补充。最初 `make check`、`make test` 和原有 39 个 E2E 全部通过；追加失败路径后仍发现可重复的后端缺陷。修复与回归测试一并保留，不能将原有的通过数理解为后端不存在缺陷。

## 执行与结果口径

```sh
make check
make test
make test GOFLAGS='-race -count=1'
make e2e MODULE=all

# 单模块定向回归
make e2e MODULE=M04
make e2e MODULE=M06
make e2e MODULE=M09

# 给每个用例文件设置硬超时，包含 import 和 run(context)
make e2e MODULE=all E2E_ARGS='--case-timeout 120'
```

新增 6 个测试类、24 个测试方法，由 6 个 `run(context)` 用例入口执行。全量入口由 39 个增至 45 个；24 是新增测试类内的场景数，不能与 45 个用例入口直接相加。部分测试方法还使用 subTest 覆盖多种非法组合。

| 模块 / 测试类 | 方法数 | 验证内容 |
| --- | ---: | --- |
| M00 / HarnessBoundaryTests | 5 | 无输出和无换行启动超时、健康检查失败清理、导入超时、缺失结果、错误返回类型、异常退出、断言失败和挂起后的真实服务进程清理、错误二进制路径、禁用 Python 优化模式 |
| M03 / PracticeBoundaryTests | 4 | 结束会话后 SIGKILL 重启重试及摘要冲突、非法结束原因、8 路同时重复作答只记一次事实/复习/反馈、练习与考试并发创建只占一个活动槽 |
| M04 / ExamBoundaryTests | 5 | 迟到事件触发交卷并持久化、过期主动提交采用不完整遥测语义、过期版本不能放弃新草稿、已接受事件交卷后重启仍可重放、交卷后同键不同正文冲突 |
| M06 / GenerationLeaseTests | 5 | 过期租约不能完成、重复完成不能重复扣待处理计数、单个未结算调用不能冒充三次质检、调用阶段/候选编号/评估身份核验、空评估维度不能发布 |
| M08 / EvidenceRetryTests | 1 | 证据处置和重建只执行一次，SIGKILL 重启后取回同一处置/重建标识，同键正文变化冲突，新会话存在时仍可读取已接受回执 |
| M09 / BackupDestinationTests | 4 | 拒绝以源目录为备份输出、已有备份原样保留、恢复不能破坏自己的输入、已有非活动数据和嵌套文件不能被覆盖 |

## 真实进程与数据不变量

- 新增测试类统一启动正式 `bin/avocab`，不依赖 `e2e` build tag 或测试专用 HTTP 路由。既有模块测试仍可使用 `bin/avocab-e2e` 验证故障注入和算法时间轨迹。
- 每个测试方法独占数据目录、随机本地端口、服务进程及客户端上下文。业务创建、作答、交卷、任务完成、证据处置通过真实 HTTP；备份/恢复通过真实 CLI。
- 数据库检查以只读连接核对事实数量、状态、revision、待处理计数、账本和投影版本。仅截止时间与租约到期通过明确的 SQLite 夹具设置；这部分属于带受控夹具的灰盒测试，并非纯黑盒时间等待。
- 重启场景使用真实 SIGKILL 和同目录重启。它覆盖进程异常退出及 SQLite 已提交数据恢复，不等同于断电、磁盘损坏或任意指令点故障注入。
- 并发测试通过线程屏障同步发送 HTTP，核对全局活动槽以及一次业务提交对应的 attempt/review/exposure 数量。
- 发布验证同时保留合法路径：三个独立、已成功结算且绑定当前候选的调用和完整六维评估能发布；非法元数据不写候选和题目，缺失语义维度的候选作为拒绝结果留痕。
- 迟到的预算结算仍按冻结契约允许匹配原 owner/fence/input 的请求入账。结算许可不赋予旧 worker 发布或完成任务的权利。

## 已复现并修复的行为

| 类别 | 修复前可观测行为 | 修复后要求 |
| --- | --- | --- |
| 超时交卷 | 返回 `EXAM_SUBMITTED` 后考试仍为 open，交卷副作用被错误返回触发回滚 | 先提交交卷事务，再拒绝迟到动作；报告在重启后仍唯一存在 |
| 考试遥测 | deadline 之后主动 submit 仍走普通提交，可能生成不适用的学习证据 | 过期提交统一标记 telemetry_incomplete，排除相应 FSRS 更新 |
| 版本并发 | 旧 expected_revision 可以 abandon 新草稿 | 409 REVISION_CONFLICT，保持会话和草稿 |
| 请求重放 | 结束练习、证据处置、交卷后的历史作答重试返回关闭/版本冲突；交卷同键变更正文反而成功 | 持久化成功回执，先核对摘要，再进行新写入状态检查 |
| 输入错误 | 非法结束 reason 返回 500 | 400 INVALID_REQUEST，状态不变 |
| 生成完成 | 过期 lease 可完成；重复完成会再次扣 pending_count | 有效 lease 校验；每个 item/worker/fence 绑定唯一完成正文和回执 |
| 发布门禁 | reserved 调用被重复引用为 generator/evaluator；错阶段、错候选和空 ratings 可发布 | 核对 stage、item、owner/fence、attempt/candidate、成功结算、不同 evaluation_index、profile 及六维完整结果 |
| 备份输出 | 先 RemoveAll 目标，可能覆盖源库或已有备份 | 按契约只输出到新目录，不删除既有目标 |
| 恢复输入 | input 与 target 相同时先删除自己的 app.db；可以覆盖已有非活动数据 | 目标在取得进程锁后必须为空，仅允许锁文件 |
| 验收器 | PIPE.readline 可令启动超时失效；用例同步执行可永久挂起；缺失 status 默认通过 | 文件日志轮询、单调时钟、独立用例进程与进程组清理、明确返回状态、超时也落盘失败报告 |

证据处置回执保存不可变的 disposition/rebuild 标识，并读取该重建的持久化状态；重试不会另外创建或执行一个重建。重建失败后的恢复仍走已有维护恢复入口。被业务拒绝的请求不占用幂等键，保持冻结契约“仅成功保存”的要求。

## 验收产物

每轮结果位于 `artifacts/e2e/run_<UTC时间>_<随机ID>/<模块>/result.json`。每个用例都有 `case.log`、`case-result.json`，新增类另有 `scenarios.log` 和每个方法的 `http.json`、各次启动的 `process-N/server.log`。HTTP 记录不保存 Cookie、CSRF 或 worker token；内部 worker 协议的失败响应和数据库断言保留在场景日志。

已保存的修复前证据：

- `artifacts/e2e/regression-before/result.json`：初批 12 个场景中 10 个失败，覆盖 M03/M04/M06/M08。
- `artifacts/e2e/run_20260909_043813_111f2391/M06/result.json`：发布凭据与空评估维度的失败证据。
- `artifacts/e2e/run_20260909_044102_915c2377/M09/result.json`：备份/恢复目标保护的 4 个失败场景。

已通过的全量回归：`artifacts/e2e/run_20260909_044616_5413b961/all/result.json`，45 passed、0 failed、0 skipped。

## M11 与实时模型质量的边界

普通 `make e2e` 中 QL-01/QL-02 只审核已有 `artifacts/quality-benchmark/report.json` 及本地账本协议，结果明确标记 `historical_live_report_audit` / `historical_live_report_and_local_ledger`。缺失报告会失败，不生成假报告、不跳过，也不自动发起外部模型调用。

本轮发现旧 QL-01 会在缓存 30 分钟过期后自动执行真实模型基准；一次全量回归触发了该路径，被新增 120 秒硬超时终止，未完成新的实时验收。后续全量通过结果使用已有历史报告。

新实时评测必须显式执行：

```sh
make quality-live
# 或显式调用 CLI 并传 --allow-paid
```

该入口允许真实付费请求。Python API 默认 `allow_paid=False`，CLI 不带 `--allow-paid` 会失败。QL-03 验证这一开关。

本轮补全了 evaluator 提示的 grammar 和 explanation_consistency，使其与后端六维要求一致，并将评估 profile 标识绑定实际执行配置。历史模型报告不能证明更新后提示或当前模型依然满足授权指标；需要重新运行显式实时基准后才能作出新的模型质量授权结论。当前通过结论限于列出的后端不变量、进程恢复和可重复的本地测试。
