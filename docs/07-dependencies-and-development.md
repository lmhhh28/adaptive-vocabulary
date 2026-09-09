# 依赖与开发环境

冻结版本1.1.0；核查及本机验证日期2026-09-08。所有依赖实际下载并在 macOS arm64 运行；其他平台是引导脚本支持范围，尚未做运行矩阵。业务应用尚待实现。

## 1. 版本与选择

| 层 | 固定版本 | 作用与依据 |
| --- | --- | --- |
| Go 工具链 | 1.27.1 | 官方归档、官方SHA-256校验；[发布记录](https://go.dev/doc/devel/release) |
| SQLite Go驱动 | modernc.org/sqlite v1.58.0 | 纯Go，无C编译依赖；[版本文档](https://pkg.go.dev/modernc.org/sqlite@v1.58.0) |
| 驱动关键传递依赖 | modernc.org/libc v1.75.6 | 与上游驱动匹配，已固定到go.mod/go.sum |
| Go实际SQLite | **3.53.4** | 运行SELECT sqlite_version()/sqlite_source_id()实测；不是系统sqlite3版本 |
| FSRS | go-fsrs/v4 v4.0.0 | FSRS-6、21权重；[官方release](https://github.com/open-spaced-repetition/go-fsrs/releases/tag/v4.0.0) |
| Python工具 | uv 0.12.10 | 项目内安装；[官方PyPI版本](https://pypi.org/project/uv/0.12.10/) |
| Python | CPython **3.13.15** | uv管理版，worker/.python-version固定补丁版本 |
| Python实际SQLite | **3.53.1** | 标准库sqlite3实测，独立于Go驱动 |
| 工作流 | langgraph **1.2.11** | [官方包](https://pypi.org/project/langgraph/1.2.11/) |
| 检查点 | langgraph-checkpoint-sqlite **3.1.1** | [官方包](https://pypi.org/project/langgraph-checkpoint-sqlite/3.1.1/)；同步SqliteSaver作为首版实现基线 |
| 数据验证 | pydantic **2.13.5** | 严格结构校验，extra=forbid；[官方包](https://pypi.org/project/pydantic/2.13.5/) |
| HTTP | httpx **0.28.1** | Go内部协议及可配置模型HTTP调用；[官方包](https://pypi.org/project/httpx/0.28.1/) |
| 界面 | Go标准库 + 浏览器原生ES modules/CSS | 无Node、npm、Vite、React依赖；不引用第三方CDN |
| 浏览器验收工具 | Python Playwright **1.62.0** / Chromium **151.0.7922.34**（revision 1234） | 独立e2e环境与uv.lock；只作测试工具，不增加应用Node构建链 |

Go的精确模块图见 [go.mod](../go.mod)、[go.sum](../go.sum)。Python直接依赖见 [pyproject.toml](../worker/pyproject.toml)，全部传递依赖及分发包哈希见 [uv.lock](../worker/uv.lock)。本机安装41个Python包；不要手工编辑锁文件。LangChain core、LangSmith等由LangGraph传递引入，不代表项目需要开通相应云账户。设置 `LANGSMITH_TRACING=false`，生成内容不自动上传追踪服务。

依赖采用真实可获取的稳定版本，不以网页“latest”作为锁。uv与Go官方分发内容由下载来源及校验确认；Python本体由固定uv版本选择的managed distribution提供。系统Python 3.9.6仅用于首次引导，不能运行worker，因为其SQLite及Python版本不属于冻结基线。

## 2. 安装、同步、检查

在仓库根目录执行：

```sh
make bootstrap
make sync
make check
```

`bootstrap` 需要已有Python3.9+、pip和网络，将Go与uv放在 `.tools/`，校验现有工具版本，不修改shell配置或系统工具。`sync` 下载Go模块，并用固定Python和 `uv sync --locked` 建立 `worker/.venv/`。缓存和managed Python都留在项目 `.cache/` / `.tools/`。`check` 使用临时数据库，结束即清理，不接触未来的学习历史。

`make content` 下载并核验固定ECDICT候选快照，保存到 `data/sources/ecdict/<commit>/`（大文件不进Git），同时保存LICENSE和manifest.json；已有文件先校验哈希并复用。首次下载需网络，本机已完成。这个命令不将原始释义发布为正式sense/题目。

当前另可运行make tasks、make task ID=M00、make e2e-tools和make check-browser。e2e-tools在e2e/.venv与.tools/playwright安装固定依赖/浏览器；check-browser真实启动Chromium、加载本地HTTP页面并点击，通过后保存artifacts/browser-check/result.json、browser.png和trace.zip。00/05/08/09中的业务HTTP、avocab命令及模块make e2e仍待实现，不能把工具链探针称作业务E2E。

更新依赖时：编辑版本声明及bootstrap/.python-version → 用新工具执行 `go mod tidy` / `uv lock --project worker` → 比较传递依赖与许可变化 → 更新参数快照/运行记录 → 执行全部检查。涉及FSRS输出或SQLite/序列化格式变更，要追加回放/恢复验证，不能只更新文本版本号。正常开发使用locked同步，不重新求解。

## 3. 本次验证记录

| 检查 | 实际结果 | 证明范围 |
| --- | --- | --- |
| `make bootstrap` | Go1.27.1、uv0.12.10已安装 | 项目隔离工具可执行 |
| `go mod verify` | 所有模块校验通过 | 已下载内容匹配go.sum |
| Go SQLite | 3.53.4；WAL、foreign_keys=1、busy_timeout=5000、synchronous=2 | 实际驱动及连接配置成立 |
| Go事务/快照 | 回滚记录不进入VACUUM INTO快照，快照integrity_check=ok | 备份底层能力可用；不等于完整产品备份实现 |
| FSRS | 21权重/学习步长与配置完全一致；Again→同日Good→跨日Good回放相同 | 包API、同日/跨日路径和固定输入确定性可用；不证明业务资格或学习效果 |
| Python SQLite | 3.53.1 | worker运行库满足WAL最低要求 |
| LangGraph | interrupt后关闭连接，用新连接及新graph恢复，最终state正确 | SQLite持久化与恢复API可用 |
| HTTP/Pydantic | MockTransport回包校验、未知字段拒绝 | 离线依赖联通，无外部模型请求 |
| 样例/文档 | 8 lexeme、9 sense、原13题加组卷补充1题；2结构负例、4待语义评估负例；本地链接检查 | 14题结构基线；语义模型验收尚未执行 |
| 组卷样例 | 三题型均衡有1份可行组合；原样例、英文干扰提示、重复目标三个负例均无解 | 穷举原始fixture关系，不是HTTP组卷器测试 |
| 任务清单 | 12个模块，无依赖环/责任重叠，M00可立即开始实现 | 所有业务模块状态仍planned |
| 浏览器工具 | Playwright1.62.0、Chromium151.0.7922.34，本地HTTP点击通过，trace/截图已保存 | 真实浏览器工具可用，不是产品E2E |

实测SQLite source_id：

```text
Go:     2026-07-24 19:02:57 bf7c7f30031888f4e796e429ab3978879485813aaca6f641c7b33e4e09459bcc
Python: 2026-05-05 10:34:17 c88b22011a54b4f6fbd149e9f8e4de77658ce58143a1af0e3785e4e6475127e9
```

两者独立达到SQLite>=3.51.3的WAL修复基线，且不要求彼此版本相同；检查点与业务库不同文件。[SQLite官方WAL说明](https://sqlite.org/wal.html)

## 4. 内容与生产模型依赖

已提供 [原创fixture](../fixtures/content-v1.json)，不用外部词典账户。它有同词多义、跨库共享、三任务、同unit三种形式、结构及语义负例，适合开始业务实现。

1.1.0另有[独立来源的组卷补充](../fixtures/exam-assembly-v1.json)，保留原fixture不变，同时提供可行三题型卷与无解对照。当前策略升级为observation-v2/selection-v2/budget-v2；旧policy-v1保留，FSRS参数未调整。任务与E2E入口见[09](09-implementation-plan.md)。

ECDICT候选导入固定1.0.28实际提交 `8defb761f7c7ad1818ca94290a1844d7b33d6b23`。该版本是附注tag，tag对象 `64b6edbb...` 不是源码commit；下载验证时已纠正并分别记录。完整CSV、许可文本和统计清单已在本机保存，来源清单见 [content-sources.json](../config/content-sources.json)。词频、CET-6标签与中文释义分别处理，不把原始数据等同已核验标准词义。[ECDICT官方仓库](https://github.com/skywind3000/ECDICT)、[固定版本许可](https://github.com/skywind3000/ECDICT/blob/8defb761f7c7ad1818ca94290a1844d7b33d6b23/LICENSE)

实际原始数据：65,936,699字节；770,611行（不含表头），raw word按原文去重也是770,611，重复0行；非空中文translation为768,739行；tag按空白分隔精确匹配cet6得到5,407行/词。不对大小写或词形归一后的唯一性、CET-6完整覆盖或释义准确率作外推。CSV SHA-256为 `d0ce61e560b50d9905d20de3173aa3ca80950ce235bedd21c53e025cf9f38cb0`，许可文件哈希保存在来源清单。

生产生成使用 [generation-profile.example.json](../config/generation-profile.example.json)，默认disabled/fixture。两个模型profile均需填入明确HTTPS endpoint、model及环境变量key名称，模型接口适配边界已写入配置；不包含真实密钥。请求/token硬上限在 [policy-v2.json](../config/policy-v2.json)，按00实现预留预算。启用前需完成30负例/15正例的固定检查集及供应商配置验证，不能用依赖检查程序的成功代替。

`LANGGRAPH_STRICT_MSGPACK=true` 在worker启动前设置；检查点仅序列化JSON兼容业务数据，不反序列化任意Python对象。内部API bearer token由未来服务生成并通过本机0600文件提供。应用 `.env` 不提交，当前 `.env.example` 仅说明配置，尚未实现dotenv加载器。

首版没有Redis、Docker、外部数据库、任务队列或云平台账户前置条件；全量词库和生产模型是内容扩充阶段的输入。
