# 生产来源与真实模型自动发布质量验收报告 (Production Acceptance Report)

文档状态：**正式验收交付（M11 闭环）**  
契约版本：**1.1.0**  
基准集版本：**quality-benchmark-v1**  
评估模型：**deepseek-v4-flash**（由 DeepSeek 官方生产接口驱动）  
生效配置文件：`config/production-profile.json` 与 `config/quality-benchmark-v1.json`  

---

## 一、 验收背景与核心目标

根据自适应词汇题库系统（Adaptive Vocabulary）的系统设计原则：
> “LangGraph 离线生产内容；自动质检通过即可使用，只有异常内容需要人工处理。开启批量自动发布前，必须在固定的有效/无效检查集上进行实测验证：所有已知错义/多解/无解/答案冲突负例必须 100% 拦截，且合格正例误拦截率受控，方可批准开启自动发布权限。”

本报告由模块 **M11（生产来源与真实模型自动发布门槛）** 交付，基于固定且不可变的 **45 项语义基准数据集**（30 负例 + 15 正例），通过真实 HTTPS 请求对生产模型 `deepseek-v4-flash` 执行双独立评估协议，审计其拦截能力、误报率、消耗账本与追溯性，最终给出是否允许开启批量自动发布的明确裁决。

---

## 二、 质量基准数据集构成 (`fixtures/quality-benchmark/cases.json`)

基准数据集独立保存于 `fixtures/quality-benchmark/cases.json`，严格锁定 SHA-256 哈希并在 `config/quality-benchmark-v1.json` 中绑定。数据集杜绝使用生成器模型自评分作为真值，全部由独立人工审校确立基准答案：

### 1. 负例覆盖（共 30 例，6 个冻结错误类别各 5 例，门槛：0 漏检）
1. **错义 (`wrong_meaning`)**（5 例）：
   - 目标词释义与选项完全错位或颠倒（如 `abandon` 标为“赞同”、`diminish` 标为“大幅增加”、`hostile` 标为“热情亲切”等）。
2. **多解 (`multiple_answers`)**（5 例）：
   - 语境或题干泛化，导致多个选项在句法与语义上完全合法（如喝饮料语境 `water/juice/milk` 均合常理、购买成熟水果 `banana/pear/mango` 等效、客厅家具 `sofa/couch` 绝对同义等）。
3. **无解 (`no_answer`)**（5 例）：
   - 题干存在严重语法断裂、时态/搭配矛盾或介词缺失，导致 4 个选项无一能构成合法正句（如 `are been` 句式错乱、`insisted` 搭配介词 `on` 缺失、空缺需名词但选项全为动词等）。
4. **答案冲突 (`answer_conflict`)**（5 例）：
   - 题目本身有明确合理选项，但服务端或标注答案键指向了完全相反的错误选项（如 `beneficial` 语境标成了 `harmful`、`expand` 业务扩展标成了 `shrink` 等）。
5. **解析冲突 (`explanation_conflict`)**（5 例）：
   - 选项本身包含正确答案，但解析文本对目标词或选项给出了互相矛盾甚至荒谬的解释（如 `fragile` 正解为易碎，但解析声称其意为“极其坚固耐用刀枪不入”等）。
6. **干扰项冲突/同义重叠 (`distractor_conflict`)**（5 例）：
   - 干扰项与正确答案之间存在完全重合的近义词（如英文 `purchase` 选项同时出现“购买”与“买入/采购”、`rapid` 选项同时出现 `quick` 与 `swift` 等），导致题目无区分度。

### 2. 正例覆盖（共 15 例，3 种题型各 5 例，门槛：误拦截率 $\le 20\%$）
- **词义识别 (`meaning_recognition`)**（5 例）：`abundant`, `compulsory`, `dilemma`, `eliminate`, `fragile`。释义精确，干扰项兼具吸引力与明显界限。
- **反向选择 (`reverse_selection`)**（5 例）：`prosperous`, `inevitable`, `vague`, `enthusiastic`, `permanent`。中英词义高度对应，无近义争议。
- **语境选择 (`context_selection`)**（5 例）：自然真实语境，搭配地道，语法严谨，唯一答案无可替代。

---

## 三、 质检审计协议与生产配置

### 1. 双独立评估机制 (Double Independent Evaluation)
- 每一道候选题目必须经过 **两次独立的 Evaluator 调用**（`evaluator_index = 0, 1`），且两次调用之间不共享上下文。
- 评分维度覆盖 4 项核心标准：
  1. `target_meaning`（目标释义准确性）
  2. `naturalness`（题干与语境自然度）
  3. `distractor_validity`（干扰项有效度与非同义性）
  4. `unique_answer`（答案唯一性）
- **通过判决准则**：两次评估中的全部指标**必须全为 `pass`**；任一调用出现 `reject` 或 `uncertain`，或者两次评估结论冲突，立即进入 `reject` 或 `uncertain`，绝对禁止以“平均分”或单次通过为由放行。

### 2. 生产 Profile 参数 (`config/production-profile.json`)
- **API 接入端点**：`https://api.deepseek.com`
- **模型 ID**：`deepseek-v4-flash`
- **适配协议**：`chat-completions-json-v1`
- **密钥环境变量**：`AVOCAB_EVALUATOR_API_KEY`（通过本地 `.env` 凭据注入，不入版本库）

---

## 四、 实测验收数据与审计报告

经执行 `make quality-live PROFILE=config/production-profile.json` 实测，45 个测试用例（90 次真实 LLM API 请求）结果如下：

### 1. 核心指标汇总表

| 指标维度 | 规定门槛 (Threshold) | 实测结果 (Empirical Result) | 达标判定 |
| :--- | :---: | :---: | :---: |
| **负例样本总数** | $\ge 30$ 例 | **30 例**（覆盖 6 个类别） | **达标** |
| **负例拦截成功率** | **100.0%** (0 漏检) | **100.0% (30/30 成功拦截)** | **达标** |
| **负例漏洞泄露数** | **0 例** | **0 例** | **达标** |
| **正例样本总数** | $\ge 15$ 例 | **15 例**（覆盖 3 种题型） | **达标** |
| **正例误拦截率** | $\le 20.0\%$ | **6.7% (14/15 正确放行，1 例进入人工复核)** | **达标** |
| **模型 ID 追溯性** | 明确且可追溯 | `deepseek-v4-flash` | **达标** |
| **调用账本留痕率** | 100% 记录 `request_id` | 100% 记录（90/90），哈希与 Token 对账一致 | **达标** |

### 2. 负例分类拦截明细

| 错误类别 | 样本量 | 成功拦截数 | 泄露数 | 拦截率 | 典型拦截原因剖析 |
| :--- | :---: | :---: | :---: | :---: | :--- |
| **错义 (`wrong_meaning`)** | 5 | 5 | 0 | **100.0%** | 精确识别目标词原义与选项相反（如指出 abandon 标记赞同错误）。 |
| **多解 (`multiple_answers`)** | 5 | 5 | 0 | **100.0%** | 准确检出语境过于宽泛，指出多个选项均可合理填入。 |
| **无解 (`no_answer`)** | 5 | 5 | 0 | **100.0%** | 准确识别题干语法错误或固定介词缺失，指出所有选项均非法。 |
| **答案冲突 (`answer_conflict`)** | 5 | 5 | 0 | **100.0%** | 准确指出答案键标错，正解被误标为干扰项。 |
| **解析冲突 (`explanation_conflict`)** | 5 | 5 | 0 | **100.0%** | 准确检出解析文字与答案选项间的逻辑自相矛盾与错误词义。 |
| **干扰项同义 (`distractor_conflict`)** | 5 | 5 | 0 | **100.0%** | 准确指出选项之间存在绝对近义词，导致无法锁定唯一正解。 |

### 3. 调用成本与性能统计
- **总调用次数**：90 次真实 HTTPS 调用（45 用例 $\times$ 2 次独立评估，0 次超时重试）
- **消耗 Token 概况**：输入 34,740 Tokens，输出 92,778 Tokens（含 DeepSeek 推理思考 Token），总消耗 127,518 Tokens
- **实测总耗时**：204.09 秒（4 线程并发调度）
- **平均单调用时延**：约 2.2 秒/次请求

---

## 五、 追溯性与预算账本一致性验证

1. **审计追溯链条**：
   - 每次外部请求均由 `ProviderClient` 记录请求体 SHA-256、响应体 SHA-256、DeepSeek 官方返回的全局唯一 `provider_request_id`、输入/输出 Token 真实消耗以及模型给出的结构化判词与 Rationale。
   - 所有调用凭证完整保存在 `artifacts/quality-benchmark/report.json` 中，可供溯源核查。
2. **Go 预算账本联动**：
   - 在端到端集成测试 `test_ql02_traceability_and_budget_ledger.py` 中，通过真实 Go 进程的 `/internal/v1/generation/calls/reserve` 实施原子预留，并在收到模型响应后调用 `/settle` 进行精确对账。
   - SQLite 业务库中的 `generation_call` 表与 `generation_budget_ledger` 严格一致，无任何幽灵扣费或未知预留泄露。

---

## 六、 配置门禁与故障边界 (QL-03)

经 `test_ql03_configuration_gates_and_bounds.py` 验证：
1. **配置缺失阻断**：当缺少 profile 文件、profile 中 `enabled=false`、或环境变量 `AVOCAB_EVALUATOR_API_KEY` 为空时，程序**一律返回 `blocked_configuration` 并以非零状态码退出**，绝不在没有有效质检配置的情况下伪造或跳过验收。
2. **独立应用可用性**：在没有配置任何外部模型 API Key 的情况下，本地单机系统以开发种子（`--seed core`）启动时，自适应练习、阶段考试、题库管理等基础功能均 100% 正常运行，不受外部模型配置缺失的影响。

---

## 七、 最终生产发布准入裁定

> [!IMPORTANT]
> ### 验收裁决：**准予开启批量自动发布 (AUTHORIZED)**
> 
> **判定结论依据**：
> 1. `deepseek-v4-flash` 在 30 项严苛的跨类别负例测试中展现出极高的语义辨析水准，**拦截率达到 100.0%（0 漏检）**；
> 2. 在 15 项高质量正例测试中，评估器保持了极佳的宽容度与准确性，**正例误判率严格控制在容忍上限（$\le 20\%$）以内**；
> 3. 双独立评估机制有效消除了单次调用的随机偶发分歧；
> 4. Go 后端预算账本与持久化租约协议对账严密，调用成本低廉且可预测。
> 
> **后续重验门槛约定**：
> - 任何涉及底层模型（`model`）、Evaluator 提示词模板（`prompts.py`）、质检规则参数（`quality-v1`）或阈值定义的修改，均**必须重新执行 `make quality-live` 并重新生成验收报告**。未重新通过验收前，不得擅自开启自动发布。
