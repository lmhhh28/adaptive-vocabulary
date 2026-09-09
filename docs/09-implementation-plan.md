# 可启动模块与端到端实施任务

版本：1.1.0，2026-09-08冻结。任务清单机器来源为[implementation-plan-v1.json](../config/implementation-plan-v1.json)。本文拆分实施工作，不改变00/08/10业务契约。

## 当前能运行什么

当前已可运行make bootstrap/sync/check/content、make tasks、make task ID=Mxx，以及make e2e-tools/check-browser。后两条验证独立Python Playwright/Chromium工具链，探针访问自己的本地HTTP页面，**不是业务产品E2E**。业务服务、模块CLI、run-generation-lab、quality-live和make e2e MODULE=Mxx由以下任务交付；当前不能据任务卡上的命令声称已实现。

每张卡可用make task ID=Mxx输出自包含实施要求。本次冻结的12张卡均为planned，只有M00无业务前置，可立即开始实现；“可独立启动”指模块完成并满足明确前置后，可用卡片命令单独启动/验收，不要求等完整产品做完。这里没有创建新的Codex任务。

## 共同交付与启动契约

- 业务仍为一个Go模块化单体。模块是可交付流程/计算组件，不拆成独立数据库或微服务；Python worker是独立进程。
- M00建立make build（正常二进制bin/avocab）及make e2e MODULE=Mxx（测试构建bin/avocab-e2e）。make run可封装serve；M07建立make run-generation-lab，M11建立make quality-live。未知模块、缺实现、零用例、缺依赖或必要配置一律非零退出，不能skip后显示通过。
- serve --data-dir <dir> --listen 127.0.0.1:<port> --policy config/policy-v2.json。默认8080；测试可用端口0，由操作系统分配，在stdout输出一行JSON ready={base_url,pid,schema_version,contract_version,policy_versions,data_dir}。就绪前完成迁移、连接参数校验及过期考试结算；health不得提前报ready。
- 数据目录默认data/local，由CLI显式解析绝对路径；目录0700、token文件0600，app.db、process.lock、worker.token位于该目录，checkpoint位于同目录独立文件。生产绑定只接受127.0.0.1。未知schema、policy或较新的contract版本拒绝启动；已有数据不自动重置。
- 服务和任何修改同一业务库的CLI共用process.lock。写库CLI发现服务运行返回SERVICE_RUNNING；恢复操作只写新目录。服务内内容处置/重建由同一Go进程执行；故障后的CLI resume/abort/restart先停止服务再取得同一锁。备份create/verify不得另开业务写入口，输入采用一致性只读快照能力。
- GET /client-context建立本机浏览器会话并返回csrf_token；Host/Origin校验同08，响应no-store，token不得出现在日志。HTTP验收先取得cookie/token，再按真实CSRF协议请求，不能关闭认证来让测试通过。
- --seed core为显式开发种子，仅对新/空业务库执行；重复同种子幂等，非空用户库拒绝seed覆盖。core包含content-v1和exam-assembly-v1；exam-conflict仅原content-v1。生产默认不seed，不触发模型。
- e2e构建才能启用确定性时钟/随机输入/故障点。入口用单独测试token，拒绝非loopback；正常构建无该路由/命令。正常UUID仍随机，测试固定输入只用于可复现验收。e2e改变测试超时或请求额度时必须标识独立test-profile，不能改policy-v2冒充冻结策略。

## E2E统一证据与隔离

验收器每次建立artifacts/e2e/<run-id>/<module>/，独立data-dir、空数据库、端口、浏览器context及provider/checkpoint；启动真实进程，轮询带超时的ready/health，再通过真实HTTP或浏览器操作。不复用用户正在运行的服务或真实学习库。成功/失败均记录种子、时钟、policy/schema/contract、进程版本和命令。

必须产出result.json（run_id/module/status/cases/passed/failed/skipped/versions/start-end/exit_codes/artifact_paths）、服务/worker/provider日志、浏览器用例trace.zip/截图及必要数据库状态摘要。必测用例skipped>0不可判模块完成；进程崩溃/断言失败非零退出，finally回收自己创建的进程，失败保留目录。日志不收集真实key/答案请求正文；受控fixture核对通过测试代码读取隔离库，不能增加生产答案旁路。

浏览器采用独立[e2e/pyproject.toml](../e2e/pyproject.toml)与[e2e/uv.lock](../e2e/uv.lock)，Python 3.13.15、Playwright 1.62.0、其固定Chromium；不增加应用Node构建链。实现依据[Playwright Python官方文档](https://playwright.dev/python/docs/intro)，测试工具与worker生产依赖分别管理。M02是CLI组件E2E，M06是worker协议组件E2E，M07的外部模型可以使用真实本地HTTP脚本provider；这些都必须明确层级，不能代替M10产品浏览器验收或M11真实模型语义验收。

## 依赖与并行安排

| M00 | 运行底座与真实进程验收器 | 无 | 真实Go进程 + SQLite + HTTP + Chromium |
| M01 | 标准内容、固定种子与题库管理 | M00 | 真实浏览器 → HTTP → SQLite |
| M02 | 观测、FSRS、冷却及选择引擎 | M01 | 真实CLI子进程 → 隔离SQLite事实 → 真实FSRS → 回放（组件E2E，非浏览器流程） |
| M03 | 训练全流程与纠错 | M01, M02 | 真实Chromium → 训练HTTP → SQLite事实/FSRS |
| M04 | 考试组卷、改答与交卷 | M01, M02 | 真实多页面Chromium → 考试HTTP → SQLite/FSRS |
| M05 | 来源导入、词表建库与异常处置 | M01 | 真实文件上传/浏览器 → 导入HTTP → SQLite |
| M06 | 生成任务服务、租约与预算账本 | M01 | 真实Go + SQLite + 独立HTTP脚本worker（协议组件E2E） |
| M07 | Python离线生产与自动质检闭环 | M06 | 真实Go + 真实Python/LangGraph + 独立HTTP provider + 双SQLite |
| M08 | 内容证据处置、报告修订与重建恢复 | M02, M03, M04 | 真实浏览器/API + Go重建 + 停服务CLI恢复 + SQLite回放 |
| M09 | 完整备份、新目录恢复与对账 | M07, M08 | 真实服务/worker → 文件备份CLI → 全新目录 → 重启后继续作答 |
| M10 | 个人使用全流程与浏览器发布验收 | M03, M04, M05, M07, M08, M09 | 真实Chromium完整产品E2E，复用真实Go/SQLite/worker |
| M11 | 生产来源与真实模型自动发布门槛 | M05, M07, M08 | 真实生产provider/来源验收，显式配置和额度门槛 |

推荐批次：M00 → M01 → 并行M02/M05/M06 → 按就绪情况并行M03/M04/M07 → M08 → 并行M09/M11 → M10。严格依赖以机器清单为准，批次只是执行建议；M11需生产profile/key/预算，缺失不会阻塞fixture应用启动，但完整“生产自动发布已验收”必须等M11真实通过。

共享修改约定：M00拥有进程入口、核心事务接口和测试生命周期；模块通过自己的routes/cli注册与业务代码接入。后续需要改共享接口时，由集成负责人串行完成并同步调用方，不能各自复制实现。SQL迁移按任务前缀追加，已应用迁移不得改写；模块只拥有自己的文件范围。所有人需保留仓库中其他人的修改。

基础表责任同样提前确定：M00建立session、idempotency_record、projection_control与全局序号；M01建立内容、题库及question_instance；M02建立共享attempt/response/exposure/review/event_revision事实容器、投影及纠错状态；M03只增加训练专属状态，M04增加考试条件、草稿和assessment_result。共享事实不能外键依赖尚未交付模块的专属表；由专属关联表引用共享事实。这样M03与M04不需要抢改同一迁移，也不互为启动前置。

生成实验室使用显式test-profile及loopback HTTP provider，仅允许隔离e2e服务与worker启用；生产profile仍强制HTTPS。实验室测试身份不能用于正常服务启动，生产配置缺失不能自动转到测试provider。M07应实际演示服务拒绝生产job引用test-profile。

必须共用一个事务的操作不可跨任务异步拼接：训练评分/事实/投影/纠错/反馈/游标；考试交卷/最终草稿/评分/观测；证据处置/维护/水位；调用租约检查/预算预留。M02提供接受调用方TxnContext的接口，不私自提交外层事务。M03与M04可并行实现，但共同session唯一索引和幂等组件由M00提供。

## 六项修订与验收映射

| 修订 | 负责模块 | 必测ID |
| --- | --- | --- |
| R01 多标签事件重排与未知重试 | M04 | EX-02 |
| R02 持久预算/未知消耗 | M06、M07、M09 | GE-02、WK-02、BK-02 |
| R03 abort维护及restart | M08、M09 | RB-02、BK-03 |
| R04 当前实例排除与cutoff | M02，M03/M04整合 | OB-01、PR-02、EX-03 |
| R05 提前复习资格矩阵 | M02、M03 | OB-02、PR-03 |
| R06 组卷可行/无解样例 | 当前契约检查、M04 | EX-01 |

任务完成标准是代码、启动命令、真实验收结果和必要说明同时交付。只有编译、单测、stub、截图或本地probe不能将业务模块标complete。每模块先完成其最小可用页面；M10只整合与完善，不把全部前端推迟到最后。

## M00 运行底座与真实进程验收器

目标：交付可启动的单机Go服务、SQLite迁移/锁/配置、统一传输约束及真实子进程E2E生命周期。

前置：无业务模块前置。责任范围：`cmd/avocab/`、`internal/platform/`、`internal/core/`、`migrations/000_runtime.sql`、`e2e/runner.py`、`e2e/cases/M00/`、`e2e/support/lifecycle.py`、`e2e/support/http_client.py`、`e2e/support/clock.py`。

实施步骤：

1. 实现serve/doctor与结构化ready输出、loopback动态端口、数据目录权限、SQLite逐连接参数和同一进程锁；未知schema/policy失败即停。
2. 固定共享TxnContext、时钟/UUID/随机源接口、幂等及维护入口；生产构建不暴露测试时钟/故障控制。
3. 交付最小首页、client-context、health；实现启动/就绪/浏览器/进程重启/清理和证据输出的E2E runner，缺用例或缺实现必须非零退出。

实现后的真实启动入口：

```sh
./bin/avocab serve --data-dir artifacts/manual/M00 --listen 127.0.0.1:8080 --policy config/policy-v2.json
make e2e MODULE=M00
```

验收层级：真实Go进程 + SQLite + HTTP + Chromium。

- PL-01：新目录启动→浏览器首页与health→重启；schema/contract/policy/SQLite版本可核对。
- PL-02：第二服务进程、非loopback、非法Origin/CSRF、未知schema被拒绝，原目录不受破坏。
- PL-03：e2e runner对零用例、启动失败、超时、断言失败非零退出；所有测试子进程可回收。

完成产物：二进制、迁移与启动命令；共享接口和测试控制约定；M00结果JSON、浏览器trace/截图、真实进程日志。未满足前置时可实现本模块纯函数或适配器，但不能宣称本模块E2E完成。

## M01 标准内容、固定种子与题库管理

目标：通过真实服务建立标准词义、不可变题目版本与可测试题库，支持不依赖模型的开发种子。

前置：M00。责任范围：`internal/content/`、`internal/bank/`、`migrations/010_content.sql`、`web/templates/content/`、`web/static/content/`、`e2e/cases/M01/`。

实施步骤：

1. 实现来源/lexeme/sense/form/option身份登记、唯一约束及版本快照；提供core和exam-conflict显式seed，旧版本正文不改写。
2. 实现sources/banks读写、成员移除/archive与基础内容明细；统一revision/幂等和活动会话/维护检查。
3. 交付题库列表/详情/创建/成员管理最小页面，展示题型可用数和来源。

实现后的真实启动入口：

```sh
./bin/avocab serve --data-dir artifacts/manual/M01 --listen 127.0.0.1:8080 --seed core
make e2e MODULE=M01
```

验收层级：真实浏览器 → HTTP → SQLite。

- CT-01：首次导入core→查看14题及两个题库→重复seed不增生身份/版本；未知seed失败。
- CT-02：跨库引用同sense，移除/归档不删除全局词义与历史引用。
- CT-03：标点/答案键修订保持form，语义改变新form；重复选项/未知来源拒绝。

完成产物：完整固定题库和可访问管理页面；内容身份/幂等证据；core种子可供后续任务复用。未满足前置时可实现本模块纯函数或适配器，但不能宣称本模块E2E完成。

## M02 观测、FSRS、冷却及选择引擎

目标：提供由真实事实驱动的纯计算及同事务投影接口，冻结在线/回放一致结果，支持独立CLI组件E2E。

前置：M01。责任范围：`internal/learning/`、`internal/selection/`、`migrations/020_learning.sql`、`e2e/fixtures/learning/`、`e2e/cases/M02/`。

实施步骤：

1. 实现10 R04/R05矩阵、时间cutoff、普通/纠错/改答分组及主混淆分母；原事实不保存永久资格结论。
2. 适配真实go-fsrs库，保存完整Card和版本；实现候选分组/稳定排序、5/3不同lexeme冷却及纠错状态。
3. 交付e2e专用verify-trace命令，完整输入内容/事实写入全新隔离数据库，再回放并输出状态摘要；该命令不允许写用户库，生产构建不提供测试事实导入。

实现后的真实启动入口：

```sh
./bin/avocab-e2e learning verify-trace --input e2e/fixtures/learning/ordinary-early.json --output artifacts/manual/M02
make e2e MODULE=M02
```

验收层级：真实CLI子进程 → 隔离SQLite事实 → 真实FSRS → 回放（组件E2E，非浏览器流程）。

- OB-01：首次/重复form、24小时内外、本次反馈、考试选择后的曝光标签与在线/回放一致。
- OB-02：五个不同early_review释放普通冷却；纠错不推进普通间隔，普通位置/待巩固/混淆并集符合矩阵。
- OB-03：普通错+两次纠错产生三个review；同日/跨日、未答/改答/失效资格及完整Card可复现。
- SL-01：探索位1/5/9、due/unseen/early互补、稳定并列排序、无内容和小题库阻塞结果确定。

完成产物：供训练/考试共用的TxnContext内投影接口；固定事实trace与期望摘要；无业务服务替身的CLI组件E2E结果。未满足前置时可实现本模块纯函数或适配器，但不能宣称本模块E2E完成。

## M03 训练全流程与纠错

目标：交付可在浏览器持续练习、刷新恢复、即时反馈并穿插纠错的完整流程。

前置：M01、M02。责任范围：`internal/practice/`、`migrations/030_practice.sql`、`web/templates/practice/`、`web/static/practice/`、`e2e/cases/M03/`。

实施步骤：

1. 实现session/practice创建恢复、next保留与提交的单事务组合；调用M02更新投影，不自行复制学习算法。
2. 交付题库选择→题目→单选提交→反馈→下一题→结束/放弃页面，处理pending、重试和无候选状态。
3. 实现纠错form次数/间隔/队列与跨会话曝光；加入事务故障点和发题/结束/retire竞争测试。

实现后的真实启动入口：

```sh
./bin/avocab serve --data-dir artifacts/manual/M03 --listen 127.0.0.1:8080 --seed core
make e2e MODULE=M03
```

验收层级：真实Chromium → 训练HTTP → SQLite事实/FSRS。

- PR-01：实际点击回答→反馈→后续题→结束；未提交前网络/HTML/恢复/错误均无答案字段。
- PR-02：提交成功响应丢失后重试、换键、并发同答案只产生一次attempt/review/feedback；不同答案冲突。
- PR-03：关闭页面/重启服务恢复同实例/选项；普通错+穿插+两种新form纠错正确执行。
- PR-04：提交中途故障所有事实/投影/队列/游标回滚；发题与结束/停用竞争不会保留非法新实例。

完成产物：最小完整训练UI和API；事务/恢复E2E结果；真实数据可由学习查询和回放核对。未满足前置时可实现本模块纯函数或适配器，但不能宣称本模块E2E完成。

## M04 考试组卷、改答与交卷

目标：交付固定试卷、持久待确认队列、多标签收敛、限时评分和学习观测的浏览器闭环。

前置：M01、M02。责任范围：`internal/exam/`、`migrations/040_exam.sql`、`web/templates/exam/`、`web/static/exam/`、`e2e/cases/M04/`。

实施步骤：

1. 实现配额共同求解、lexeme去重、跨题提示和有界搜索，记录seed与完整实例快照。
2. 实现10 R01事件协议、服务器草稿、gap屏障、回执恢复、手动/自动交卷单事务；调用M02处理合格观测。
3. 交付组卷→选择/clear/改答→保存状态→交卷→报告UI；与M03共同验证单活动session。

实现后的真实启动入口：

```sh
./bin/avocab serve --data-dir artifacts/manual/M04 --listen 127.0.0.1:8080 --seed core
make e2e MODULE=M04
```

验收层级：真实多页面Chromium → 考试HTTP → SQLite/FSRS。

- EX-01：core默认三题三任务实际组卷成功；exam-conflict返回无解；搜索上限返回不同错误。
- EX-02：同水位双标签竞争、明确拒绝重排、接受后丢响应、unknown原样重试、gap屏障迟到请求，按10 R01验证。
- EX-03：先错后对/先对后错/clear/noop最终评分与首次正确性准确；合格单次选择才更新FSRS。
- EX-04：deadline等时、关闭页面、重启过期结算、提交/最后选择竞争和跨模式创建不违反唯一槽位。

完成产物：可真实完成的三题型考试与报告；多标签和断线trace；组卷/评分/投影一致性证据。未满足前置时可实现本模块纯函数或适配器，但不能宣称本模块E2E完成。

## M05 来源导入、词表建库与异常处置

目标：从基础来源和TXT/CSV词表完成可追溯建库，异常可在页面处置，独立于学习和生成进程。

前置：M01。责任范围：`internal/importer/`、`migrations/050_import.sql`、`web/templates/import/`、`web/static/import/`、`e2e/fixtures/import/`、`e2e/cases/M05/`。

实施步骤：

1. 实现固定ECDICT适配及小型原始来源fixture，校验来源/哈希/规范化冲突并保存统计。
2. 实现TXT/CSV与source entries不同输入契约、异步进度、resolved/ambiguous/not_found/invalid/excluded。
3. 交付导入预览、异常选择已有标准sense或排除、来源修订后重新解析流程；不能将混排释义直接发布为标准sense。

实现后的真实启动入口：

```sh
./bin/avocab serve --data-dir artifacts/manual/M05 --listen 127.0.0.1:8080 --seed core
make e2e MODULE=M05
```

验收层级：真实文件上传/浏览器 → 导入HTTP → SQLite。

- IM-01：UTF-8 TXT/CSV真实上传→自动匹配/异常→adopt/exclude→题库成员与原输入可追溯。
- IM-02：重复上传幂等、大小/行数限制、重复JSON键、来源冲突、多义混排和非法引用被正确处理。
- IM-03：小型固定来源统计有golden结果；全量ECDICT另做离线导入验收，不在每次E2E重复下载。

完成产物：导入和异常处置页面；固定输入/预期统计集；实际文件上传验收与来源审计。未满足前置时可实现本模块纯函数或适配器，但不能宣称本模块E2E完成。

## M06 生成任务服务、租约与预算账本

目标：在Go提供可供真实独立worker调用的任务协议、持久调用预算与受控发布；使用脚本worker验证网络边界。

前置：M01。责任范围：`internal/generation/`、`migrations/060_generation.sql`、`web/templates/generation/`、`web/static/generation/`、`e2e/support/scripted_worker.py`、`e2e/cases/M06/`。

实施步骤：

1. 实现固定job/item、claim/renew、fence、预算reserve/settle及candidate/complete；维护期间候选/预算可继续，发布暂停。
2. 将10 R02原子预留、未知消耗、迟到结算、预算超额及无checkpoint恢复落实为持久状态机。
3. 交付任务/预算/异常摘要页面；用独立HTTP脚本worker调用服务，所有正式内容仍由Go校验并发布。

实现后的真实启动入口：

```sh
./bin/avocab serve --data-dir artifacts/manual/M06 --listen 127.0.0.1:8080 --seed core
make e2e MODULE=M06
```

验收层级：真实Go + SQLite + 独立HTTP脚本worker（协议组件E2E）。

- GE-01：任务领取/续租/过期接管，旧fence不能发布；双评估完整pass才可发布。
- GE-02：最后一次额度并发抢占、调用后崩溃、usage缺失/迟到/超预留、网络尝试上限及预算恢复，按10 R02核对。
- GE-03：候选/结算响应丢失幂等；维护期间候选可保存而正式发布暂停；实例只能引用正式版本。

完成产物：可实际连接的worker协议；持久预算账本和进度UI；供应商发送授权与预算对账证据。未满足前置时可实现本模块纯函数或适配器，但不能宣称本模块E2E完成。

## M07 Python离线生产与自动质检闭环

目标：用真实Python/LangGraph进程串起Go任务、真实HTTP模型适配、双评估、候选发布和崩溃恢复。

前置：M06。责任范围：`worker/avocab_worker/`、`e2e/support/provider_server.py`、`e2e/fixtures/provider/`、`e2e/cases/M07/`。

实施步骤：

1. 实现worker启动配置、token文件、HTTP JSON adapter和LangGraph固定item checkpoint，供应商调用前后执行预算/租约协议。
2. 实现生成/两次独立评估调用/有限修订/uncertain-conflict异常，保存所有获得的候选及关联调用记录。
3. 构建本地HTTP脚本provider，提供确定性合格/拒绝/冲突/超时/断线/缺usage场景；不是MockTransport，不替换Go或SQLite。

实现后的真实启动入口：

```sh
make run-generation-lab DATA_DIR=artifacts/manual/M07
make e2e MODULE=M07
```

验收层级：真实Go + 真实Python/LangGraph + 独立HTTP provider + 双SQLite。

- WK-01：固定job→真实provider网络请求→两次评估→Go发布→内容可供实例引用，调用账本逐次可追溯。
- WK-02：生成后/供应商已响应但候选未提交时强制终止worker，恢复后不复用未知调用授权、不重复发布。
- WK-03：reject有限修订，uncertain/conflict停异常，租约丢失/预算耗尽停止发送；无凭证普通启动仍fixture可用。

完成产物：可启动worker与本地生成实验室；跨进程故障恢复记录；确定性协议质量验收；不宣称真实模型语义质量已通过。未满足前置时可实现本模块纯函数或适配器，但不能宣称本模块E2E完成。

## M08 内容证据处置、报告修订与重建恢复

目标：将历史错误内容的隔离/失效/恢复、报告revision和投影重建接成可恢复的真实流程。

前置：M02、M03、M04。责任范围：`internal/maintenance/`、`internal/revision/`、`migrations/080_revision.sql`、`web/templates/quality/`、`web/static/quality/`、`e2e/cases/M08/`。

实施步骤：

1. 实现disputed/invalidate/restore_validity与统一维护闸门、读取stale标志、重建运行和原子切换。
2. 实现10 R03的policy_only/evidence_revision、resume/abort/restart与进程重启恢复；原报告与事实不可变。
3. 交付异常/影响范围/原报告和修订报告/维护状态页面；CLI写库与服务使用同一独占进程锁。

实现后的真实启动入口：

```sh
./bin/avocab serve --data-dir artifacts/manual/M08 --listen 127.0.0.1:8080 --seed core
make e2e MODULE=M08
```

验收层级：真实浏览器/API + Go重建 + 停服务CLI恢复 + SQLite回放。

- RB-01：活动session期间处置整个事务拒绝且不写修订；结束后处置原子进入维护并重建。
- RB-02：invalidate后失败→abort仍维护→重启仍维护→restart成功才解闸；policy_only abort可安全恢复。
- RB-03：无效题从证据/FSRS/混淆中排除，原成绩不变，修订报告分母正确；全void比率null；restore重新一致。

完成产物：内容错误可真实处理的管理流程；两类abort状态证据；在线/回放/报告revision对账。未满足前置时可实现本模块纯函数或适配器，但不能宣称本模块E2E完成。

## M09 完整备份、新目录恢复与对账

目标：通过实际文件快照和全新目录恢复，证明事实、活动实例、幂等、投影、预算与可选checkpoint可续用。

前置：M07、M08。责任范围：`internal/backup/`、`e2e/cases/M09/`。

实施步骤：

1. 实现backup create/verify/restore，快照水位从快照本身读取、manifest/哈希/原子完成目录。
2. 新目录验证引用与SQLite，作废所有未完成租约并递增fence；保留预算unknown占用；可选checkpoint单独对账。
3. 恢复后启动真实服务和worker，继续原活动流程；覆盖维护中备份与缺checkpoint恢复。

实现后的真实启动入口：

```sh
./bin/avocab backup verify --input artifacts/manual/M09/backup
make e2e MODULE=M09
```

验收层级：真实服务/worker → 文件备份CLI → 全新目录 → 重启后继续作答。

- BK-01：实际创建练习/考试历史及未完成生成任务→备份→新目录恢复→恢复实例/草稿/幂等/报告。
- BK-02：恢复不带checkpoint仍保持调用预算，旧worker不能发布，重新领取不重用未知调用预留。
- BK-03：损坏哈希/路径穿越/未知manifest或schema/活动目标目录拒绝；维护状态备份恢复不意外解闸。

完成产物：可执行备份恢复CLI；独立恢复目录及校验清单；恢复后完整流程与回放证据。未满足前置时可实现本模块纯函数或适配器，但不能宣称本模块E2E完成。

## M10 个人使用全流程与浏览器发布验收

目标：把各模块最小页面整合为个人可连续使用的本机产品，完成干净目录端到端验收和操作手册。

前置：M03、M04、M05、M07、M08、M09。责任范围：`web/templates/shell/`、`web/static/shell/`、`e2e/cases/M10/`、`docs/runbook.md`。

实施步骤：

1. 整合导航、键盘操作、错误/保存/无候选/维护状态、窄屏布局；业务规则复用模块实现。
2. 以干净目录通过真实UI完成建库→训练→考试→异常处理→重建→备份恢复→继续使用。
3. 交付安装/启动/停止/恢复说明和完整验收产物；检查所有内容载荷、缓存和管理旁路的答案边界。

实现后的真实启动入口：

```sh
./bin/avocab serve --data-dir artifacts/manual/M10 --listen 127.0.0.1:8080 --seed core
make e2e MODULE=M10
```

验收层级：真实Chromium完整产品E2E，复用真实Go/SQLite/worker。

- APP-01：全新目录从页面完成个人主要流程，刷新/服务重启可继续，无手工改数据库。
- APP-02：活动会话期间管理/历史反馈/错误/缓存等内容边界符合冻结规则，控制台无未处理错误。
- APP-03：键盘单选/提交、保存中/失败重试、窄屏和维护恢复可操作；产物标明环境和未通过项。

完成产物：完整本机产品和操作手册；全流程浏览器trace/截图/结果；fixture产品验收；与生产语义门槛分开记录。未满足前置时可实现本模块纯函数或适配器，但不能宣称本模块E2E完成。

## M11 生产来源与真实模型自动发布门槛

目标：在明确配置与预算下验证真实provider及固定质量检查集，决定是否可开启批量自动发布。

前置：M05、M07、M08。责任范围：`fixtures/quality-benchmark/`、`config/quality-benchmark-v1.json`、`e2e/cases/M11/`、`docs/production-acceptance.md`。

实施步骤：

1. 扩充至少30负例/15正例并按冻结类别覆盖，核验基准答案及来源，避免用生成器自评分当真值。
2. 配置固定generator/evaluator profile和预算，通过真实HTTPS调用完成盲评/误拦截报告；不把本地脚本provider通过算为语义验收。
3. 验收成功后记录provider/model/prompt/质量策略与检查集哈希；变更时重新验证。

实现后的真实启动入口：

```sh
make quality-live PROFILE=/absolute/path/to/production-profile.json
make e2e MODULE=M11 PROFILE=/absolute/path/to/production-profile.json ALLOW_PAID=1
```

验收层级：真实生产provider/来源验收，显式配置和额度门槛。

- QL-01：所有已知错义/多解/无解/答案冲突负例拦截，至少每类5个、合计30负例与15正例；报告正例误拦截数/比例。
- QL-02：固定profile、真实模型ID/usage/调用账本和prompt版本可追溯；预算与故障策略一致。
- QL-03：缺profile/key/明确预算则返回blocked_configuration且非零退出，不跳过后标通过；fixture应用仍可启动。

完成产物：固定语义检查集；真实模型质量/成本报告；可否开启批量自动发布的明确验收结论。未满足前置时可实现本模块纯函数或适配器，但不能宣称本模块E2E完成。
