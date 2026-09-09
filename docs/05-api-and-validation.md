# 接口、验证与实施顺序

状态：冻结规格1.1.0，API尚未实现。本页是路由与验证总览，省略共同前缀/api/v1；请求字段、统一幂等、错误码、恢复/放弃和内部协议已固定在 [08](08-http-contract.md)。全局状态机与维护/备份边界见 [00](00-frozen-design.md)。

## 1. API 边界

| 接口 | 输入/用途 | 关键行为 |
| --- | --- | --- |
| `POST /sources`、`GET /sources/{id}` | 名称、定位信息、来源版本及可用范围说明 | 登记标准数据源，与普通建库词表分开 |
| `GET /client-context`、`GET /maintenance` | 本机CSRF上下文；不含内容的维护进度 | 同源/no-store；按09启动契约与10恢复状态显示 |
| `POST /sources/{id}/imports` | 外部条目 ID、词形/词义、统计口径及来源版本 | 建立可追溯基础词库条目；重复导入按外部身份和版本去重 |
| `POST /banks` | 名称和备注 | 创建个人题库 |
| `POST /banks/{id}/imports` | 原始词表、可选目标义、使用的来源引用与解析策略 | 返回已解析和异常项，不隐去来源冲突 |
| `GET /imports/{id}/items`、`POST /import-items/{id}/resolve` | 查看解析结果、为异常项指定目标词义或排除 | 保存原输入、候选、人工处置和版本 |
| `POST /banks/{id}/generation-jobs` | 固定的已解析范围、生成配置 | 返回 job_id，立即结束请求 |
| `GET /generation-jobs/{id}` | 任务进度 | 显示合格、异常、重试和已发布数量 |
| `GET /question-candidates/{id}`、`GET /quality-findings` | 查看候选、质检依据和待处理问题 | 可按 job/candidate/status 筛选 |
| `POST /quality-findings/{id}/resolutions` | expected_revision、修订/排除/重新生成及处理理由 | 修订产生新候选并重新检查，通过后自动发布，保留处置历史 |
| `POST /question-versions/{id}/retire`、`POST /question-versions/{id}/revisions` | 停用原因或修订内容 | 正文不可原地修改；修订重新进入候选校验流程 |
| `POST /practice-sessions` | bank_id | 创建或恢复一个活动练习会话 |
| `POST /practice-sessions/{id}/next` | expected_cursor | 幂等保留一个题目实例，无候选返回原因 |
| `POST /practice-sessions/{id}/attempts` | instance_id、一个 option_id、submission_id、遥测 | 按快照评分，成功后返回反馈 |
| `POST /practice-sessions/{id}/end` | expected_revision | 结束会话，保留历史及未解决复习标记 |
| `POST /exams` | bank_id、题量、任务配额、可选限时/标签 | 固定组卷并按 lexeme 去重；不足则失败并返回可用范围 |
| `POST /exams/{id}/response-events` | instance_id、选择/取消、client_event_id、expected_revision | 有序记录每次选择，无评分反馈 |
| `POST /exams/{id}/submit` | expected_revision、submission_id | 冻结最终答案，评分并派生观测 |
| `GET /exams/{id}/result` | 已提交考试 | 返回最终分数、题型统计及改答分析 |
| `GET /learning/senses/{id}` | 词义 | 返回各 task_family 的证据和调度状态 |
| `/internal/generation/...` | worker 领取、续租、提交 | 按逻辑任务和租约幂等，Go 验证后落正式内容 |

exam response-events 是事实追加接口，当前答案视图从它派生；不能只提供覆盖式 PUT 草稿而丢掉首次选择。尚未确认的网络写入不能靠前端显示“已保存”掩盖。

合格候选自动发布是 Go 内容模块中的幂等状态转换，由 worker 结果接收或异常处置后的重新校验触发；没有跳过检查的通用 publish 接口。所有处置写入携带幂等键和被修改对象的 expected_revision。

## 2. 发题与判题

发题 DTO 只含 instance_id、题干、task_family、选项 ID/文本/顺序及必要媒体。答案键、正确标记、解析和内部选择统计只保留在服务端。

训练只接受实例内的一个选项；未知ID、多个选项、缺失选项和未知字段拒绝。有效提交无论对错均返回二值评分、正确答案与简短解析。刷新或重试不能改变题目、顺序或再次应用模型更新。

考试可以交卷前改选或取消；全卷统一评分，正确 1 分、错误或未答 0 分，报告总分与答对比例。最终结果与首次选择的正确性分别展示，不声称首次选择就是首次提取。

考试同一 lexeme 仅一个目标题；三任务配额共同求解，不能独立抽题后靠重复补足。组卷还应检查显著跨题提示，不能承诺完全消除语义关联。每次随机试卷未做等值，报告不暗示不同卷分数完全可比。

## 3. 会话和时间

个人版冻结为跨模式仅一个open会话，通过统一session表唯一部分索引保证。关页面保留会话，GET /sessions/active恢复；开始另一模式前结束练习或提交/放弃考试。无自动会话租期，后台生成可并行。

放弃考试保留已经发生的选择/暴露，标记 unsubmitted，不根据未答项生成错误复习。考试若有限时，服务器保存截止时间，关闭页面不暂停时限；超时由同一幂等交卷逻辑完成。

服务器序号处理提交顺序，客户端时间只作遥测。到题显示时间、首选时间、改选时间、最终交卷时间语义不同。考试复习时间不能统一替换成整卷交卷时刻。

## 4. 必须验证的行为

| 用例 | 预期 |
| --- | --- |
| 请求伪造 correct/score 或提交实例外选项 | 服务端忽略伪造评分，非法选择被拒绝 |
| 同一训练提交重复、并发或换幂等键 | 只记一次正式作答、一次符合策略的模型更新 |
| 发题响应丢失后重试、刷新页面 | 返回同一实例和选项顺序 |
| 某词跨题库出现 | 同一 sense/task 状态共享，三个 task 不自动互相加分 |
| 正常、提前、纠错、改答观测同时有曝光条件 | 多维条件不互相覆盖，统计分组正确 |
| 普通错误 + 两次不同纠错提交 + 任意网络重试 | 三个业务事件按策略各更新一次，重试和队列毕业不增加更新 |
| 考试先错后对、先对后错、取消再选择 | 时间线完整，最终评分准确，模型按策略处理而非重复复习 |
| 草稿写入与交卷竞争 | revision/终态约束保证最终结果可解释 |
| 组卷题量或配额不足 | 明确失败，不重复目标词、不静默更改配额 |
| sibling 冷却阻塞小题库 | 返回约束原因，不死循环；纠错例外按独立规则验证 |
| 纠错缺替代形式、达到重试上限 | 保留待复习状态，不把洗牌当新形式 |
| 仅改答案键、标点，与真正替换干扰语义 | 按形式身份判定表处理，不能用修订版本伪造新形式 |
| 无提交、改答、争议/无效题和纠错中的干扰项 | 主分母及各观测分组符合资格规则，回放一致 |
| worker 失败恢复、旧租约迟到、发布响应丢失 | 不重复发布，保留各尝试及处置 |
| 内容停用/修订、题库删除 | 历史快照仍可判读；修正必须留下明确记录 |
| 事件回放或备份恢复 | 活动状态与固定版本结果相符，可定位差异 |

测试应验证这些实际不变量，不编写只复述实现的测试。固定时间和随机输入，覆盖同日/跨日、时区边界和相同时间戳；FSRS 库自带单元测试不能代替业务观测适配测试。

## 5. 备份、恢复、模型回放

备份采用 SQLite 一致性快照机制，例如 Online Backup API。运行中的 WAL 数据库只复制 app.db 不能保证已提交事务都在备份内。[S09](06-sources.md)

备份清单包含业务库、必要题目/候选附件、schema 版本、程序/算法版本、参数集合与事件水位。检查点可单独备份；两库不是一个原子事务，恢复后依据 Go 作业记录对账，再按幂等协议续跑。

恢复验证：数据库完整性与外键检查、内容/事件数量、活动试卷快照、幂等键、模型水位。至少演练一次恢复到新目录并重放投影；没有恢复验证的复制不视为完成备份能力。

模型回放使用原题目、原事实和明确选定的修订/策略版本，不重新生成题目。重建新投影后再切换；保留失败原因和旧活动状态。

首版采用本机 CLI 管理备份与回放，以下是待实现的命令契约，不是当前已可运行的命令：

| 命令契约 | 输入 | 产物与验收 |
| --- | --- | --- |
| `avocab backup create --output <directory>` | 输出到新目录，指定是否包含生成检查点 | `manifest.json`、业务库快照及必要附件；清单含版本、事件水位、文件校验和 |
| `avocab backup verify --input <directory>` | 备份目录 | 结构/校验和/数据库完整性与外键报告，失败返回非零退出码 |
| `avocab backup restore --input <directory> --target <new-directory>` | 已验证备份和非活动新目录 | 恢复产物及对账报告；不覆盖运行中的库 |
| `avocab learning rebuild --policy <version> --projection <new-version>` | 固定策略/参数、目标投影版本 | model_rebuild_run、事件数量和状态摘要；校验成功后切换 |
| `avocab learning rebuild resume/abort --run <id>` | 指定运行；写库CLI要求服务已停止并取得同一进程锁 | 10 R03规定的mode、requires_rebuild与next_action；abort不等于解闸 |
| `avocab learning rebuild restart --run <id> --projection <new-version>` | 中止后仍需重建的运行 | 新run/目标version，成功验证后才解闸 |

CLI 输出机器可读结果，包含 operation_id、status、input_manifest/policy、event_watermark、checks 和 errors；具体字段 schema 随第一阶段接口契约一起固定。恢复后启用服务前，先执行验证和必要的作业对账。

## 6. 实施阶段与完成标准

实施顺序、独立启动和文件责任以[09的12个模块](09-implementation-plan.md)为唯一任务清单：M00运行与验收底座，M01内容题库，M02学习引擎，M03训练，M04考试，M05导入，M06生成任务/预算，M07worker，M08证据处置/重建，M09备份恢复，M10完整浏览器流程，M11生产质量门槛。训练与考试共用M02接口并可并行，每个业务模块随同最小页面交付，不把全部前端推迟到最后。

六项独立审查修订必须分别通过10中EX-02、GE-02、RB-02、OB-01/02和09中EX-01验收；真实E2E必须启动实际Go/SQLite/必要Python进程，记录真实HTTP/浏览器结果。当前make check只增加fixture可行性与任务/冻结完整性校验，仍不证明业务实现已通过。

首版完成意味着作答事实正确、题库可生产、练习与考试闭环可靠，不等于已证明记忆算法最优或准确测量全部词汇能力。
