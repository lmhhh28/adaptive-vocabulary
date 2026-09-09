# Adaptive Vocabulary 运行与个人使用手册 (Runbook)

本文档是 **Adaptive Vocabulary（自适应词汇题库系统）** 的完整个人使用与运维操作手册，覆盖从源码构建、首次初始化、日常学习/考试使用、词表导入、质量处置、投影维护、到全量数据备份与灾难恢复的全生命周期流程。

前端开发请阅读[接口联调指南](frontend-api.md)和[接口字段参考](frontend-api-reference.md)，其中包含当前请求响应、重试规则及与冻结契约的差异。全部文档见[索引](README.md)。

---

## 一、 系统简介与设计理念

Adaptive Vocabulary 是一款专为个人设计的本机单体自适应词汇学习与评测系统。

- **客观表现驱动**：根据用户在题目中的实际客观作答（首次选择、改答、提交时间、最终正确性）自动调度下一道题目，**不要求用户主观评估“认识 / 模糊 / 不认识”**，也不要求制定强制的每日学习计划。
- **三类四选一题型**：
  1. 词义识别（`meaning_recognition`）：英文目标词 $\rightarrow$ 选择中文释义；
  2. 反向选择（`reverse_selection`）：中文释义 $\rightarrow$ 选择英文目标词；
  3. 语境选择（`context_selection`）：英文例句填空 $\rightarrow$ 根据语境选择英文词。
- **科学间隔算法**：集成 FSRS 调度算法作为时间基线，学习状态跨题库共享（以 `(sense_id, task_family)` 为学习单元），并实施严格的冷却机制（普通题间隔 5 个其他单词，纠错间隔 3 个其他单词）以及 1/5/9 探索位机制。
- **全链路防泄题保证**：在线判题和调度完全在本地 Go 服务内以原子事务完成；未提交前页面 HTML、网络 DTO 与日志绝不返回正确答案或解析；严格设置 `Cache-Control: no-store`。

---

## 二、 环境要求与引导构建

### 1. 系统要求
- **操作系统**：macOS (Apple Silicon / Intel) 或 Linux (amd64 / arm64)；
- **系统工具**：系统已预装 Python 3.9+、pip 与 `make`。
- **沙盒工具链**：项目采用自包含工具链，运行 `make bootstrap` 后会自动在 `.tools/` 安装隔离的 Go 1.27.1、uv，并在 `.cache/` 维护构建与包缓存，不污染、不替换全局系统环境。

### 2. 引导与构建步骤

```sh
# 1. 引导安装本地工具链 (.tools/go, .tools/uv)
make bootstrap

# 2. 同步并校验精确依赖锁 (go.sum, uv.lock)
make sync

# 3. 运行静态一致性与设计冻结检查
make check

# 4. 编译生产二进制 (bin/avocab) 及测试二进制 (bin/avocab-e2e)
make build
make build-e2e
```

---

## 三、 服务的启动与停止

### 1. 常用启动命令

```sh
# 首次运行：指定独立数据目录并加载核心种子词库 (core)
./bin/avocab serve --data-dir data/local --listen 127.0.0.1:8080 --seed core

# 后续日常运行：使用已有学习库启动（无需重复加载 seed）
./bin/avocab serve --data-dir data/local --listen 127.0.0.1:8080

# 或直接使用 Makefile 快捷指令
make run
```

### 2. 启动参数说明

| 参数 | 默认值 | 说明 |
| :--- | :--- | :--- |
| `--data-dir` | `data/local` | 业务数据库、进程锁与配置保存目录（自动以 `0700` 权限保护） |
| `--listen` | `127.0.0.1:8080` | 本地监听地址（生产严格限定只允许 `127.0.0.1` 本机环回地址） |
| `--policy` | `config/policy-v2.json` | 策略配置文件路径（包含 FSRS 参数、观测矩阵、冷却规则） |
| `--seed` | 空 | 仅用于空数据库初始化开发种子（支持 `core` 或 `exam-conflict`） |

### 3. 就绪信号与健康检查
- 服务启动并在完成数据库迁移与过期考试清理后，会在终端 `stdout` 输出单行 JSON 就绪信号：
  ```json
  {"ready":true,"base_url":"http://127.0.0.1:8080","pid":12345,"schema_version":80,"contract_version":"1.1.0"}
  ```
- 访问健康检查端点：
  ```sh
  curl -s http://127.0.0.1:8080/api/v1/health
  # 返回: {"status":"ok","schema_version":80,"app_version":"0.1.0","contract_version":"1.1.0","mode":"active"}
  ```

### 4. 优雅停止
- 在终端按下 `Ctrl + C`，服务将捕获 `SIGINT` / `SIGTERM` 信号，完成当前正在执行的单写事务，优雅关闭 SQLite 数据库连接并安全释放 `process.lock` 文件锁。

---

## 四、 页面功能与个人使用指南

启动服务后，使用现代浏览器（如 Chrome, Safari, Firefox, Edge）访问 `http://127.0.0.1:8080`。

### 1. 统一控制台首页（Shell Dashboard - `/`）
- **系统概览**：实时显示契约版本（`1.1.0`）、Schema 版本、SQLite 版本及运行模式（`active` 就绪或 `rebuilding` 维护中）。
- **活动会话提醒**：如果此前有未结束的自适应练习或未交卷的阶段考试，顶部会自动弹出快速恢复条，点击即可无缝继续。
- **模块导航**：快速直达“题库管理”、“自适应训练”、“阶段考试”、“词表导入”、“质量维护”与“生成实验室”。
- **快捷键指引**：列出常用键盘操作，随时按 `?` 或 `H` 键可高亮定位。

### 2. 题库管理（`/banks`）
- 查看当前系统中所有已建立的题库卡片；
- 显示每个题库的词义总量及三种题型（词义识别、反向选择、语境选择）的可用题目数量；
- 点击“开始练习”直接基于该题库发起自适应训练；
- 点击“新建题库”可自定义题库名称与备注；
- 在题库详情页（`/banks/<id>`）中可查看词义明细、添加词义或归档废弃题库。

### 3. 词表导入（`/imports`）
- **文件上传**：支持导入 UTF-8 编码的 `.txt` 文件（每行一个英文单词）或 `.csv` 文件（`word,sense_id` 格式）；
- **文本粘贴**：可直接在网页文本框中逐行粘贴生词列表；
- **自动解析与匹配**：系统自动规范化词条，并与现有标准词条数据库进行匹配；
- **异常处置**：对于多义词或未匹配的生词，导入详情页会列出异常项，支持人工指定已有标准词义（Adopt）或将其排除（Exclude）。

### 4. 自适应训练（`/practice`）
- **选题逻辑**：系统综合到期复习（Due）、未测词条（Unseen）与冷却规则，自动呈现下一题；
- **作答与提交**：
  - 点击选项或使用键盘快捷键（`1`/`2`/`3`/`4` 或 `A`/`B`/`C`/`D`）选中目标；
  - 点击“确认提交”或按下 `Enter` 键；
- **即时反馈与解析**：提交后立即展示作答结果、标准答案与详细辨析；
- **穿插纠错**：若本题回答错误，系统会为该学习单元自动生成纠错追踪，并在完成 3 个其他不同单词后穿插重测新题型；
- **结束训练**：点击右上角“结束练习”，系统将原子归档会话，未完成的纠错将安全延后至后续练习。

### 5. 阶段评测考试（`/exams/new` & `/exams/<id>`）
- **组卷配置**：选择目标题库、题量（如 3~100 题）与限时（分钟）；系统采用配额二分图算法组卷，**确保一张试卷内同一个标准单词仅出现一次**，且无跨题提示冲突；
- **答题卡与快速切换**：
  - 左侧展示完整答题卡气泡，实时指示已作答与当前题目；
  - 支持直接点击气泡跳题，或使用键盘 `←`（上一题）与 `→`（下一题）翻题；
  - 支持点击“清除选择”取消已选答案；
- **改答机制**：考试期间允许在不同选项间自由改答，所有改答行为均被审计留痕；交卷时合格改答将综合生成评分证据；
- **交卷与测试报告**（`/exams/<id>/result`）：
  - 点击“交卷”后统一评分，输出总分、正确率、作答行为特征分析（首次选择正确率、改答转对次数）以及逐题对错解析。

### 6. 质量与维护控制台（`/quality`）
- **内容证据处置**：
  - 若在练习或考试中发现题目存在内容瑕疵（如答案有争议、选项多解或语境印刷错误），可在该控制台定位目标题目；
  - 动作选择：`disputed`（标记争议并隔离主统计）、`invalidate`（确认失效并自动排除该题对成绩的负面影响）或 `restore_validity`（恢复有效）；
- **投影重建**：
  - 提交处置后系统将进入 `rebuilding` 维护模式，冻结全局写入闸门，基于不可变历史事实执行纯函数状态重算；
  - 重建验证通过后原子切换至新投影版本并解开维护闸门，保证数据绝对一致。

---

## 五、 键盘快捷键与无障碍操作指南

系统深度支持纯键盘操作，提升词汇练习与测评的流畅度：

| 快捷键 | 作用场景 | 说明 |
| :--- | :--- | :--- |
| <kbd>1</kbd> 或 <kbd>A</kbd> | 练习 / 考试 | 选中第 1 个选项（选项 A） |
| <kbd>2</kbd> 或 <kbd>B</kbd> | 练习 / 考试 | 选中第 2 个选项（选项 B） |
| <kbd>3</kbd> 或 <kbd>C</kbd> | 练习 / 考试 | 选中第 3 个选项（选项 C） |
| <kbd>4</kbd> 或 <kbd>D</kbd> | 练习 / 考试 | 选中第 4 个选项（选项 D） |
| <kbd>Enter</kbd> | 自适应练习 | **作答前**：确认提交选中项；**反馈后**：进入下一题 |
| <kbd>&larr;</kbd> (左箭头) | 阶段考试 | 切换到上一道考试题目 |
| <kbd>&rarr;</kbd> (右箭头) | 阶段考试 | 切换到下一道考试题目 |
| <kbd>?</kbd> 或 <kbd>H</kbd> | 全局页面 | 快速滚动并高亮定位键盘快捷键指引卡片 |

---

## 六、 数据备份、完整性校验与灾难恢复

系统内置企业级一致性快照备份机制，支持在线无锁快照与跨目录异地恢复。

### 1. 创建全量备份 (`backup create`)

```sh
# 创建全量备份快照并输出至指定目录
./bin/avocab backup create --data-dir data/local --output artifacts/backups/backup_$(date +%Y%m%d_%H%M%S)

# 包含 LangGraph 离线检查点（如使用离线生成）
./bin/avocab backup create --data-dir data/local --output artifacts/backups/latest --include-checkpoints
```
- **备份产物**：
  - `app.db`：通过 SQLite 原生快照技术生成的完全一致性业务数据库；
  - `manifest.json`：记录 Schema 版本、契约版本、SQLite 版本、事件水位及所有文件的 SHA-256 哈希清单。

### 2. 校验备份文件 (`backup verify`)

```sh
./bin/avocab backup verify --input artifacts/backups/latest
```
- 严格校验 `manifest.json` 的 SHA-256 哈希、数据完整性（`PRAGMA integrity_check`）与外键约束（`PRAGMA foreign_key_check`）。

### 3. 恢复至全新目录 (`backup restore`)

> [!IMPORTANT]
> 灾难恢复必须恢复至**全新、非正在运行的目标目录**，系统将自动递增 Fencing Token 并作废旧的未完成租约，防止发生数据冲突。

```sh
# 恢复到新的目录 data/restored_instance
./bin/avocab backup restore --input artifacts/backups/latest --target data/restored_instance

# 使用恢复后的新目录启动服务
./bin/avocab serve --data-dir data/restored_instance --listen 127.0.0.1:8080
```
启动后访问网页，所有历史作答记录、词库、练习进度与考试成绩均完好无损。

---

## 七、 运维排错与常见问题 (FAQ)

### Q1: 启动时报错 `process lock already held` 或 `SERVICE_RUNNING`？
- **原因**：已有另一个 `avocab serve` 进程正在使用该数据目录，或者上次进程被异常强杀（如终端断电）导致锁文件未释放。
- **排查与解决**：
  1. 检查是否有存活的后台进程：`ps aux | grep avocab`；
  2. 若确认无其他进程在运行，检查数据目录下的 `process.lock` 文件；
  3. 可运行 `./bin/avocab doctor --data-dir <dir>` 诊断数据目录状态。

### Q2: 访问练习或提交时返回 503 `MAINTENANCE`？
- **原因**：系统当前正在执行投影重建（`mode: rebuilding`）以保证数据一致性。
- **排查与解决**：
  1. 浏览器访问 `/quality` 控制台查看重建运行状态（`run_status`）；
  2. 若由于意外中断需要重置，可停止服务后在命令行运行 `./bin/avocab learning rebuild abort --data-dir <dir>`，之后重启服务即可解闸。

### Q3: 为什么创建考试时提示 `SESSION_ACTIVE`？
- **原因**：系统实施严格的跨模式排他约束（单活动槽位），防止多标签并发导致学习模型事件乱序。
- **排查与解决**：
  - 检查是否有未结束的自适应练习会话；前往 `/practice` 点击“结束练习”，释放活动槽位后再发起考试。

### Q4: 页面在手机或窄屏设备上排版是否正常？
- 系统经过严格的响应式设计测试，在 375px（iPhone SE）及以上视口宽度下均保证 `scrollWidth <= clientWidth`，无任何横向滚动条，按键均满足触控区域要求。

---

## 八、 验收测试命令汇总

为验证本机系统的全部功能符合设计规范，可随时运行下列测试套件：

```sh
# 运行底座与生命周期验证 (M00)
make e2e MODULE=M00

# 个人使用全流程与浏览器端到端验收 (M10)
make e2e MODULE=M10

# 全量回归验证 (M00 ~ M10，共 36 个端到端真实用例)
make e2e MODULE=all
```
验收日志与截图统一保存在 `artifacts/e2e/` 目录下。
