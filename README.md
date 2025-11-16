# wasm-exec-demo

`wasm-exec-demo` 由常驻的 **Coordinator** 服务与短生命周期的 **Executor** 组成。Coordinator 持续监听任务来源（当前为占位合约），根据任务提供的 CID 下载 Wasm 模块及其 JSON 输入并创建 Kubernetes Job；Executor 运行于 Job Pod，借助 wazero + WASI 执行 Wasm，并把结果写入共享卷。本指南重点介绍 Coordinator 的工作流，同时保留 Executor 在开发/调试阶段的使用说明。

---

## 架构概览
- **任务来源**：`internal/adapters/contract/placeholder.go` 输出示例任务，字段包括 `TaskID`、`WasmCID`、`InputCID`、`Entry` 等。
- **模块/输入下载**：`internal/adapters/ipfs` 可读取本地镜像目录（`COORDINATOR_IPFS_MIRROR`），也可通过 HTTP Gateway（`COORDINATOR_IPFS_ENDPOINT`）访问真实 IPFS。
- **Job 构建**：`internal/coordinator/k8s_manager.go` 以 `k8s/job.yaml` 为模板，为模块/输入创建 ConfigMap，并注入 ENTRY、INPUT_PATH 等环境变量。
- **执行输出**：`cmd/executor/main.go` 载入 `module.wasm`，解析输入 JSON/环境变量，将结果写入 `/mnt/shared/result.json` 并在日志尾行打印原始 JSON。
- **生命周期管理**：`internal/coordinator/coordinator.go` 串行处理任务、等待 Job、收集日志并发布结果，最后清理属于本次任务的 Kubernetes 资源。

---

## Coordinator

### 编译与运行
```bash
go build -o coordinator ./cmd/coordinator
COORDINATOR_NAMESPACE=default \
COORDINATOR_EXECUTOR_IMAGE=executor-demo/executor:demo \
COORDINATOR_IPFS_ENDPOINT=http://127.0.0.1:18480/ipfs \
./coordinator
```
PowerShell：
```powershell
go build -o coordinator.exe ./cmd/coordinator
$env:COORDINATOR_NAMESPACE = "default"
$env:COORDINATOR_EXECUTOR_IMAGE = "executor-demo/executor:demo"
$env:COORDINATOR_IPFS_ENDPOINT = "http://127.0.0.1:18480/ipfs"
./coordinator.exe
```
若未设置 `COORDINATOR_IPFS_ENDPOINT`，会回退到 `COORDINATOR_IPFS_MIRROR`（默认 `./host/wasm`）。

### 关键环境变量
| 变量 | 说明 | 默认值 |
| --- | --- | --- |
| `COORDINATOR_NAMESPACE` | Job/ConfigMap 所属命名空间 | `default` |
| `COORDINATOR_EXECUTOR_IMAGE` | Executor 使用的容器镜像 | `executor-demo/executor:demo` |
| `COORDINATOR_IPFS_MIRROR` | 占位 IPFS 客户端读取的本地目录 | `./host/wasm` |
| `COORDINATOR_IPFS_ENDPOINT` | IPFS HTTP Gateway 根地址 | （空） |
| `COORDINATOR_JOB_TEMPLATE` | Job 模板路径 | `k8s/job.yaml` |

### 任务流程
1. 占位合约适配器依次发出 fib/affine 等示例任务；
2. IPFS 适配器按 `WasmCID` 下载 `module.wasm`，若存在 `InputCID` 则继续拉取输入 JSON；
3. KubeManager 构建 ConfigMap，并基于模板创建 Job；
4. Executor Pod 运行 Wasm，读取 `/mnt/input/input.json`，把结果写入 `/mnt/shared/result.json` 并在日志末行输出原始 JSON；
5. Coordinator 解析日志尾行、发布结果，并删除 Job/ConfigMap。

### 示例资源
请将示例 `.wasm` 与输入 JSON 放在 `host/wasm` 目录，方便通过容器内 `/wasm` 路径执行 `ipfs add`。

| 任务 | Wasm | 输入 JSON | 说明 |
| --- | --- | --- | --- |
| `fib` | `fib.wasm` | `{"entry":"fib","args":[12]}` | 计算 `fib(12)=144` |
| `affine` | `affine.wasm` | `{"entry":"affine","args":[13,9,2]}` | 仿射变换示例 |

输入文件示例：
```
host/wasm/fib-input.json    # {"entry":"fib","args":[12]}
host/wasm/affine-input.json # {"entry":"affine","args":[13,9,2]}
```
占位任务默认使用以下 CID（若重新 `ipfs add`，请同步更新）：
- `fib.wasm` -> `QmUF8k9UKFqx55iWZyov8n1aGtNASaGafoFi3ofN6Tt1Ls`
- `fib-input.json` -> `QmbBuEbFrx1vcoPukHStpLG3G1mmX8dpSy8xUAGVFK9AdG`
- `affine.wasm` -> `QmZfTZm3UPzaVQMvxfJWdUk6KmBYTjuCAPXYxuyJnLCDrP`
- `affine-input.json` -> `Qmcs5AR22G2VFBDiqH4jK7viJeNLHvxHn9LYuw8H4tegQi`

### 本地 IPFS 演示
1. 启动仓库自带节点（监听 18480/18501）：
   ```bash
   docker compose -f ipfs/docker-compose.yml up -d
   ```
2. 将 Wasm 与输入 JSON 加入节点并记录 CID：
   ```bash
   docker exec wasm-exec-demo-ipfs ipfs add -Q /wasm/fib.wasm
   docker exec wasm-exec-demo-ipfs ipfs add -Q /wasm/affine.wasm
   docker exec wasm-exec-demo-ipfs ipfs add -Q /wasm/fib-input.json
   docker exec wasm-exec-demo-ipfs ipfs add -Q /wasm/affine-input.json
   ```
3. 运行前设置网关：
   ```bash
   export COORDINATOR_IPFS_ENDPOINT=http://127.0.0.1:18480/ipfs
   ```
   PowerShell：`$env:COORDINATOR_IPFS_ENDPOINT = "http://127.0.0.1:18480/ipfs"`
4. 完成后执行 `docker compose -f ipfs/docker-compose.yml down` 停止节点。

### 特性概览
- 顺序调度，便于调试与日志追踪；
- 模块/输入通过 ConfigMap 注入，易于复现；
- 统一输出格式：结果文件 + 日志末行 JSON；
- `DeleteArtifacts` 自动清理 Job/ConfigMap，避免 Kubernetes 资源泄漏；
- 合约/IPFS 适配器可替换为真实实现；
- `scripts/run-docker.cmd SCENARIO=add|fib|affine` 可快速演示端到端流程。

---

## Executor 概览
- 纯 Go + wazero + WASI，无需外部 C 依赖；
- 入口顺序：`ENTRY` -> `add(uint64,uint64)`（读取 `ADD_X/ADD_Y`）；
- 支持 `INPUT_PATH`/`ARGS_JSON` 提供参数，输出至 `/mnt/shared/result.txt` 或 `.json`，日志末行打印原始 JSON；
- 可用 `TIMEOUT_SEC` 控制执行超时。

本地编译示例：
```bash
go mod tidy
go build -o executor ./cmd/executor
WASM_PATH=./host/wasm/module.wasm OUTPUT_PATH=./host/shared/result.txt ./executor
```
PowerShell：
```powershell
$env:WASM_PATH   = "host\wasm\fib.wasm"
$env:INPUT_PATH  = "host\shared\input.json"
$env:OUTPUT_PATH = "host\shared\result.json"
./cmd/executor/executor.exe
```

## 目录结构
```
. 
|-- cmd/
|   |-- coordinator/
|   |   `-- main.go
|   `-- executor/
|       `-- main.go
|-- docs/COORDINATOR.md
|-- host/
|   |-- wasm/
|   `-- shared/
|-- ipfs/docker-compose.yml
|-- internal/
|   |-- adapters/
|   |   |-- contract/
|   |   `-- ipfs/
|   `-- coordinator/
|-- k8s/
|   |-- job.yaml
|   `-- pod.yaml
|-- scripts/
|   |-- run-docker.*
|   |-- run-k8s.sh
|   `-- tail-k8s.sh
`-- README.md
```

## 开发提示
- 提交前运行 `go build ./...`，确认 Coordinator/Executor 均可编译；
- `scripts/run-docker.cmd SCENARIO=fib` 可模拟任务并检查 `host/shared/result.json`；
- 建议在 `k8s/job.yaml` 中设置资源限制（如 `cpu: 500m`、`memory: 512Mi`）。

## 许可
MIT License (c) 2025
