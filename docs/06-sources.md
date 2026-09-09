# 源码与外部证据

核对日期：2026-09-08，冻结规格1.1.0。外部链接可能更新；实际依赖、参数及两套SQLite实测记录已固定在 [07](07-dependencies-and-development.md) 和锁文件中，不以网页标题代替运行版本。

## SurveyKing 源码参考

参考仓库：[SurveyKing](https://gitee.com/surveyking/surveyking)，审查提交 `d7f3b009ce39c68e7200aeed2813a1b78cb96591`。本项目未复制其 Java 实现；以下是静态审查的设计参考，不是运行测试结论。

| 仓库相对路径与行号 | 观察与取舍 |
| --- | --- |
| `server/shared/src/main/java/cn/surveyking/server/core/uitls/AnswerScoreEvaluator.java:82` | 单选按正确选项 ID 判分；借鉴服务端确定性判题 |
| 同文件 `:115`、`:157` | 多选/部分分数分派；首版四选一不移植通用问卷复杂模式 |
| 同文件 `:253`、`:274` | 填空 contain 为标准答案包含输入；不作为未来拼写判题规则 |
| `server/rdbms/src/main/java/cn/surveyking/server/impl/AnswerServiceImpl.java:769` | 服务端计算总分及逐题结果；Go 固定使用不可变实例 |
| `server/rdbms/src/main/java/cn/surveyking/server/impl/RandomSurveyProcessor.java:119`、`:188` | 保存随机试卷并恢复，借鉴固定实例；Go 用明确会话 ID |
| `server/shared/src/main/java/cn/surveyking/server/domain/dto/AnswerMetaInfo.java:23` | 原答卷级时间字段不足以表示逐题过程 |
| `server/rdbms/src/main/java/cn/surveyking/server/domain/model/UserBook.java:19` | 错题计数不能代替逐次行为历史 |
| `server/rdbms/src/main/java/cn/surveyking/server/impl/UserBookServiceImpl.java:43` | 错题写入带 Async，不能据外层事务认定所有写入原子 |
| `server/pom.xml:13`、`:26`；`client/README.md` | Java 8 / Spring Boot 2.7.7；client README 为 todo，不以完整业务前端复用为前提 |

## 官方文档与研究

| ID | 来源 | 用于支持的事实；不支持的外推 |
| --- | --- | --- |
| S01 | [FSRS 算法](https://github.com/open-spaced-repetition/awesome-fsrs/wiki/The-Algorithm)、[Anki 评分](https://docs.ankiweb.net/studying.html#answer-buttons) | D/S/R、FSRS-6 同日更新、Again/Good 使用；不证明动态四选一模型已校准 |
| S02 | [go-fsrs v4.0.0](https://github.com/open-spaced-repetition/go-fsrs/releases/tag/v4.0.0) | /v4模块、FSRS-6、21权重；已下载并验证参数、同日与跨日回放 |
| S03 | [Anki sibling](https://docs.ankiweb.net/studying.html#siblings-and-burying) | 相关题目提示效应的产品处理；不证明本项目 N=5 最优 |
| S04 | [Duolingo HLR 论文](https://research.duolingo.com/papers/settles.acl16.pdf)、[BKT 与 IRT 关系论文](https://arxiv.org/abs/1803.05926) | 可训练记忆/测量模型的后续研究方向；不作为个人冷启动即用能力模型 |
| S05 | [LangGraph workflows](https://docs.langchain.com/oss/python/langgraph/workflows-agents)、[结构化输出](https://docs.langchain.com/oss/python/langchain/structured-output) | 分发、生成评估循环、schema；不能据结构化合法性证明语义正确 |
| S06 | [LangGraph persistence](https://docs.langchain.com/oss/python/langgraph/persistence)、[interrupts](https://docs.langchain.com/oss/python/langgraph/interrupts) | 检查点与暂停恢复；不能替代 Go 业务 provenance 和发布幂等 |
| S07 | [SQLite WAL](https://sqlite.org/wal.html)、[3.51.3 发布](https://sqlite.org/releaselog/3_51_3.html) | WAL 单写者、WAL-reset 缺陷及已修复版本；不表示单人所有使用都会触发缺陷 |
| S08 | [SQLite 运行库版本](https://sqlite.org/c3ref/libversion.html)、[外键](https://sqlite.org/foreignkeys.html)、[busy timeout](https://sqlite.org/c3ref/busy_timeout.html) | 检查实际运行库，外键和等待策略按连接设置 |
| S09 | [SQLite Backup API](https://sqlite.org/backup.html)、[适用场景](https://sqlite.org/whentouse.html) | 一致性备份和低并发单机适用性；WAL 运行时不能只复制主库视为完整备份 |
| S10 | [Playwright Python安装](https://playwright.dev/python/docs/intro)、[Playwright 1.62.0](https://pypi.org/project/playwright/1.62.0/) | 独立Python浏览器验收工具；本机Chromium探针已运行，不证明业务E2E已实现 |

## 证据边界

工程文档中的 3/5 个单词间隔、两次纠错上限、探索比例和改答处理，是版本化设计默认值，不是由上述文献直接证明的效果结论。

开发内容选定为项目原创fixture；ECDICT 1.0.28固定commit的完整原始CSV及MIT许可文本已下载，SHA-256及行数/标签统计已记录。尚未逐条核验释义/来源，也不宣称官方CET-6覆盖。导入门槛及统计见 [00](00-frozen-design.md)、[content-sources.json](../config/content-sources.json)。不引入真题数据依赖。
