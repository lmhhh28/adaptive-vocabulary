# Adaptive Vocabulary

个人使用的自适应词汇题库系统。用户选择题库后直接练习或组卷考试；系统根据客观作答记录选择下一道练习题，不要求用户评价自己是否掌握，也不要求制定每日学习计划。

首版设计已冻结为 **1.1.0（2026-09-08）**，修复独立审查的六项问题，并拆成12个有依赖、文件责任、真实启动命令和端到端验收要求的实施任务。仓库包含最终设计、版本化策略、原创样例、任务清单及已锁定的依赖/浏览器检查工具；业务后端、数据库迁移、生产生成服务及前端尚待按任务实现。

## 已确认的产品边界

- 独立 Go 后端，SurveyKing 只作源码和业务设计参考。
- 单人使用，所有题库共用一份学习档案；首版不做账号、用户隔离、分享和协作。
- 三类四选一：词义识别、反向选择、语境选择。
- 用户选定题库，系统自动选择目标词、题型和题目实例。
- 必须提交选项后才能看到反馈，不提供提前查看答案的操作。
- 训练即时反馈、错题穿插重测；考试固定试卷、统一交卷评分。
- 同一标准词义与同一种测试任务的学习状态跨题库共享。
- 考试中同一个标准单词仅作为目标词出现一次，包括其不同词义和题型。
- LangGraph 离线生产内容；自动质检通过即可使用，只有异常内容需要人工处理。

## 文档入口

| 文档 | 内容 |
| --- | --- |
| [最终冻结设计与审查结论](docs/00-frozen-design.md) | 冻结基线、审查处置、确定规则及实施门槛；从这里开始 |
| [产品范围与决策记录](docs/01-product-and-decisions.md) | 产品边界、工程决策与实验边界 |
| [架构与数据模型](docs/02-architecture-and-data.md) | Go/Python 边界、业务事实、可重建状态、SQLite 与事务 |
| [学习观测与选题策略](docs/03-learning-and-selection.md) | 表现证据、FSRS、混淆、考试改答、纠错及 sibling 规则 |
| [题库与内容生产](docs/04-content-pipeline.md) | 选词导入、词义匹配、候选留痕、LangGraph、自动发布 |
| [接口、验证与实施顺序](docs/05-api-and-validation.md) | API 总览、一致性用例、备份恢复、模型回放及开发阶段 |
| [源码与外部证据](docs/06-sources.md) | SurveyKing 参考位置、官方算法和工具文档 |
| [依赖与开发环境](docs/07-dependencies-and-development.md) | 精确版本、锁文件、安装命令、实测与外部前置条件 |
| [HTTP v1 契约](docs/08-http-contract.md) | 字段、幂等、错误码、恢复与 worker 协议 |
| [可启动模块与端到端任务](docs/09-implementation-plan.md) | 12张任务卡、依赖图、文件责任、启动命令与真实E2E门槛 |
| [独立审查修订](docs/10-review-resolutions.md) | 六项修订的状态转换、资格矩阵、时间边界及验收ID |

## 设计原则

```text
不可变行为事实 + 不可变内容版本
                  ↓
          带版本的观测解释规则
                  ↓
      表现证据 / FSRS记忆状态 / 混淆记录
                  ↓
              自动选题策略
```

选择题结果能说明在特定题目和提示条件下的表现。FSRS 在首版作为时间调度基线，未校准的 R/S/D 不代表真实词汇掌握率。首次选择、最终答案、反馈后重答分别保留，不把其中任何一个直接等同于真实认知过程。

## 开发环境

macOS/Linux arm64/amd64，已有 Python 3.9+ 与 pip、make 即可引导。工具链安装到项目 `.tools/`，缓存位于 `.cache/`，不替换系统 Python 或 Go：

```sh
make bootstrap
make sync
make check
```

扩充候选词库使用 `make content`：下载并校验固定版本ECDICT原始CSV/许可，本机已完成；快照大文件保存在不入Git的data目录，可按已提交哈希复现。

`make check` 校验 Go SQLite 事务/快照、FSRS 参数与回放、Python LangGraph 检查点重开恢复，以及配置/样例/文档链接。它不是应用服务，也不证明尚未实现的 HTTP 与业务不变量已通过。验证过程不调用付费模型；首次同步需要联网下载官方依赖。

当前机器配置为[policy-v2.json](config/policy-v2.json)，保留[policy-v1.json](config/policy-v1.json)作为历史基线；FSRS权重及grading-v1不变。内容采用[原始fixture](fixtures/content-v1.json)与[组卷补充fixture](fixtures/exam-assembly-v1.json)，共14题，保留三题型组卷可行/无解对照。ECDICT仍是候选导入源；正式批量生成需要生产profile、凭证和质量检查集验收。

## 实施入口

```sh
make tasks
make task ID=M00
make e2e-tools
make check-browser
```

make tasks检查并展示依赖；make task输出自包含任务卡，不创建任务、不启动尚未实现的服务。推荐M00→M01后并行M02/M05/M06；M02后训练M03与考试M04可并行。具体文件责任和后续批次见09。

make e2e-tools安装独立锁定的Python Playwright/Chromium工具链；make check-browser真实启动Chromium并点击本地HTTP探针页面，保存trace、截图和结果。它只验证浏览器工具可用，不等于产品E2E。业务make e2e MODULE=Mxx与各模块服务命令由任务交付，未实现或零用例不得报告成功。

冻结清单为[design-freeze-v1.json](config/design-freeze-v1.json)，记录设计、策略、样例、任务和依赖锁的SHA-256。make check会核对清单；规则变更先按00/10升级版本并说明迁移，检查通过后显式重新封存。普通业务代码实现不自动改变冻结规则。
