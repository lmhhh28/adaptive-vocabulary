# 新机环境搭建与开发依赖完整指南 (Developer Setup Guide)

本文档面向需要在新机器（macOS 或 Linux）上从零开始搭建开发环境、完整启动服务并进行日常研发测试的开发者。

---

## 一、 系统架构与依赖设计理念

Adaptive Vocabulary 采用**零污染、自包含沙盒工具链**架构设计：

*   **无需全局工具**：宿主机**不需要**全局预装 Go、Node.js、npm、Docker、Redis 或任何外部数据库服务。
*   **沙盒隔离**：项目通过引导脚本自动在本地目录 `.tools/` 安装严格锁定版本的 Go (1.27.1) 与 uv (0.12.10)，并在 `.cache/` 维护构建与包缓存，绝不污染或篡改操作系统的全局环境变量。
*   **运行时基线**：
    *   **Go 端**：采用纯 Go 实现的 `modernc.org/sqlite`（内置 SQLite 3.53.4），无需本地 C/C++ 编译器（无 CGO 依赖）。
    *   **Python 端**：由 `uv` 自动下载并管理独立的 CPython 3.13.15（内置 SQLite 3.53.1），严格隔离于 `worker/.venv/` 和 `e2e/.venv/`。
    *   **前端**：React 18 + TypeScript + Ant Design v5 SPA 单页现代化架构，预编译产物通过 Go 标准库 `embed.FS` 完整内嵌至单体 Go 二进制；生产与日常运行零 Node.js 依赖。前端二次开发支持通过 `make web-build`（或在 `web/frontend/` 执行 `npm run build`）重新构建。

---

## 二、 宿主机操作系统前置要求

在运行引导脚本之前，宿主机仅需具备最基本的终端工具：

### 1. 支持的系统与架构
*   **macOS**：Apple Silicon (M1/M2/M3/M4, `arm64`) 或 Intel (`x86_64`)，macOS 12+。
*   **Linux**：Ubuntu 20.04+、Debian 11+、Fedora 36+、Arch Linux 等，架构支持 `amd64` (`x86_64`) 与 `arm64` (`aarch64`)。
*   **Windows**：请使用 **WSL2 (Ubuntu 22.04+)** 进行开发，体验与原生 Linux 一致。

### 2. 各操作系统前置软件一键安装

#### macOS
确保已安装 Xcode 命令行工具（提供 `make`、`git` 和系统 `python3`）：
```sh
xcode-select --install
```

#### Ubuntu / Debian
```sh
sudo apt update
sudo apt install -y build-essential python3 python3-pip python3-venv git curl
```

#### Fedora / RHEL / CentOS Stream
```sh
sudo dnf install -y gcc make python3 python3-pip git curl
```

#### Arch Linux
```sh
sudo pacman -S base-devel python python-pip git curl
```

---

## 三、 从零初始化与一键启动

克隆代码仓库后，请按以下步骤依次执行：

### 第 1 步：克隆仓库
```sh
git clone https://github.com/lmhhh28/adaptive-vocabulary.git
cd adaptive-vocabulary
```

### 第 2 步：引导安装沙盒工具链 (`make bootstrap`)
```sh
make bootstrap
```
*   **做了什么**：
    *   自动识别系统与 CPU 架构，从官方源下载 `go1.27.1` 归档包，核验 SHA-256 后解压到 `.tools/go`。
    *   通过系统 Python 的 pip 在 `.tools/uv` 中安装固定版本的 `uv 0.12.10`。
*   **产物**：`.tools/go/bin/go` 与 `.tools/uv/bin/uv` 就绪。

### 第 3 步：同步精确依赖锁 (`make sync`)
```sh
make sync
```
*   **做了什么**：
    *   调用 `.tools/go/bin/go mod download` 下载 Go 依赖到 `.cache/go`。
    *   调用 `.tools/uv` 自动拉取官方独立的 CPython 3.13.15 运行时。
    *   根据 `worker/uv.lock` 精确安装 41 个 Python 依赖包至 `worker/.venv/`（包含 LangGraph 1.2.11、Pydantic 2.13.5、HTTPX 0.28.1 等）。

### 第 4 步：下载离线候选词典快照 (`make content`)
```sh
make content
```
*   **说明**：从 GitHub 下载 ECDICT 1.0.28 词库快照并保存至 `data/sources/ecdict/`，下载后会自动校验 SHA-256（`d0ce61e56...`）。此步骤需联网（约 65MB）。

### 第 5 步：运行环境与冻结契约自检 (`make check`)
```sh
make check
```
*   **做了什么**：自动依次执行 Go 模块校验、代码静态分析 (`go vet`)、Go/Python SQLite WAL 兼容性测试、FSRS 算法复现性检查、离线 LangGraph 状态机恢复验证、任务卡完整性及设计冻结 Hash 比对。
*   **通过标志**：终端打印所有项 `status: ok`。

### 第 6 步：编译系统二进制
```sh
make build
make build-e2e
```
*   **产物**：
    *   `bin/avocab`：生产版 HTTP 服务及运维 CLI。
    *   `bin/avocab-e2e`：支持端到端测试时间快进、测试重置桩的 E2E 专用构建。

### 第 7 步：首次启动并加载种子词库
> ⚠️ **重要提示**：全新数据目录首次启动时，**必须加上 `--seed core` 参数**，以自动执行数据库 Schema 迁移并注入包含多义项、三题型和干扰项的基础题库！

```sh
./bin/avocab serve --data-dir data/local --listen 127.0.0.1:8080 --seed core
```

启动成功后终端将输出服务就绪信息：
```json
{"base_url":"http://127.0.0.1:8080","contract_version":"1.1.0","data_dir":"data/local","pid":12345,"ready":true,"schema_version":1}
```

现在打开浏览器访问：**[http://127.0.0.1:8080](http://127.0.0.1:8080)**，即可进入词汇学习系统。

---

## 四、 日常启动与快捷指令

首次使用 `--seed core` 完成数据初始化后，日常启动只需：

```sh
# 方式 A：使用 Makefile 快捷启动
make run

# 方式 B：直接调用二进制
./bin/avocab serve --data-dir data/local --listen 127.0.0.1:8080
```

按 `Ctrl + C` 即可优雅关闭服务（自动释放 WAL 锁与单实例排他锁）。

---

## 五、 测试与端到端验证环境

### 1. 运行单元测试
```sh
# 运行全部单元测试（Go + Python Worker）
make test

# 单独运行 Go 单元测试
make test-go

# 单独运行 Python Worker 状态机与离线节点单元测试
make test-worker
```

### 2. 搭建与运行端到端 (E2E) 浏览器测试
系统配备了完整的基于 Playwright 的无头浏览器回归测试。

#### 安装 E2E 工具与浏览器内核：
```sh
make e2e-tools
```
该命令会在 `e2e/.venv` 虚拟环境中安装 Playwright 并在 `.tools/playwright` 中下载无头 Chromium 浏览器内核。

> 🐧 **Linux 无头服务器特别注意**：
> 如果在 Linux 无桌面服务器上运行 Playwright，可能需要安装系统级的动态链接库：
> ```sh
> sudo apt-get install -y libnss3 libnspr4 libatk1.0-0 libatk-bridge2.0-0 libcups2 libdrm2 libxkbcommon0 libxcomposite1 libxdamage1 libxfixes3 libxrandr2 libgbm1 libpango-1.0-0 libcairo2 libasound2
> ```

#### 验证浏览器探针可用：
```sh
make check-browser
```
测试通过会在 `artifacts/browser-check/` 下生成截图与跟踪日志。

#### 执行现代 Web 前端 E2E 验收测试 (W00 ~ W08)：
```sh
# 运行 W08 集成、交付包装与全量回归验收
make web-e2e MODULE=W08

# 运行特定 Web 模块 (W00~W08)
make web-e2e MODULE=W01

# 运行全部 Web 端到端测试
make web-e2e MODULE=all
```

#### 执行经典真实进程 E2E 回归测试 (M00 ~ M10)：
```sh
# 运行全部经典 E2E 用例（包含网络断连、并发抢占、崩溃恢复等）
make e2e

# 指定运行特定模块
make e2e MODULE=M03      # 仅运行 M03 练习会话与重放用例
make e2e MODULE=M10      # 运行 M10 现代 SPA 交互全生命周期回归用例
```

---

## 六、 AI 内容生成 Worker 配置 (可选)

如需运行或开发基于真实大模型的离线自动出题与双盲审题功能（`worker/avocab_worker`）：

1.  **准备环境配置文件**：
    ```sh
    cp .env.example .env
    ```
2.  **配置大模型 API 密钥**：
    编辑 `.env` 文件，填入你的大模型服务凭证（或直接设置环境变量）：
    ```sh
    export AVOCAB_GENERATOR_API_KEY="sk-你的出题模型API_KEY"
    export AVOCAB_EVALUATOR_API_KEY="sk-你的审题模型API_KEY"
    ```
3.  **启动生成实验或质量基准打分**：
    ```sh
    # 启动交互式出题实验台
    make run-generation-lab

    # 执行真实模型生产基准测试（包含 30 负例 + 15 正例评测）
    make quality-live
    ```

---

## 七、 常见问题与排查手册 (FAQ)

### Q1: `make bootstrap` 报错 `No module named pip`
*   **原因**：精简版 Linux（如 Ubuntu minimal）未预装 pip 模块。
*   **解决**：执行 `sudo apt install python3-pip`（或运行 `python3 -m ensurepip`）。

### Q2: 启动报端口占用或 `lockfile acquired by pid xxxxx`
*   **原因**：之前的服务异常中断，未清理文件锁或端口 `8080` 被占用。
*   **解决**：
    1.  运行诊断命令检查并解除锁状态：
        ```sh
        ./bin/avocab doctor --data-dir data/local
        ```
    2.  检查是否已有实例在运行：`lsof -i :8080` 并结束遗留进程。

### Q3: 访问 `http://127.0.0.1:8080` 页面空白或题库数量为 0
*   **原因**：首次创建 `data/local` 时未使用 `--seed core` 灌入预制词库。
*   **解决**：
    停止服务，携带 seed 参数启动一次：
    ```sh
    ./bin/avocab serve --data-dir data/local --seed core
    ```

### Q4: 运行 `make check` 报错 `Frozen design differs`
*   **原因**：项目对架构设计文档、核心配置和锁定依赖实施了严格的 SHA-256 完整性封存。如果你修改了 `README.md`、`docs/00`~`10` 文档或策略配置，哈希会发生变动。
*   **解决**：在确认改动符合预期后，重新密封冻结清单：
    ```sh
    python3 scripts/freeze_design.py --write
    make check
    ```

### Q5: 国内网络环境下依赖下载缓慢或超时
*   **Go 模块代理**：
    在终端执行前设置：`export GOPROXY=https://goproxy.cn,direct`
*   **Python/uv 源镜像**：
    在终端执行前设置：`export UV_INDEX_URL=https://pypi.tuna.tsinghua.edu.cn/simple`
