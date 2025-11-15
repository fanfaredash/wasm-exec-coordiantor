# wasm-exec-demo

`wasm-exec-demo` 由常驻调度服务 **Coordinator** 与即时执行器 **Executor** 两部分组成。Coordinator 监听链上（目前为占位）合约的任务请求，将 Wasm 模块下载到 Kubernetes Job 中，并记录输出；Executor 运行在 Job Pod 内，通过 wazero + WASI 运行 Wasm 并把结果写回共享卷。本文档重点介绍 Coordinator 的长期运行方式，同时保留了 Executor 作为开发期验证工具的说明。

---

## 架构概览
- **任务来源**：`internal/adapters/contract/placeholder.go` 以本地样例模拟链上任务，提供 `TaskID / WasmCID / Entry / InputJSON` 等字段。
- **模块下载**：`internal/adapters/ipfs` 支持两种实现：读取本地目录（占位镜像）或通过 `COORDINATOR_IPFS_ENDPOINT` 访问真实 IPFS HTTP Gateway。
- **Job 构建**：`internal/coordinator/k8s_manager.go` 根据 `k8s/job.yaml` 模板创建 ConfigMap 和 Job，并注入 ENTRY、INPUT_PATH 等参数。
- **执行输出**：`cmd/executor/main.go` 运行 `module.wasm`，读取输入 JSON/环境变量，把结果写入 `/mnt/shared/result.json` 并在日志尾行打印纯 JSON。
- **生命周期管理**：`internal/coordinator/coordinator.go` 顺序消费任务、跟踪 Job、收集日志并清理 K8s 资源。

---

## Coordinator 服务

### 编译与运行

Linux / macOS：

```bash
go build -o coordinator ./cmd/coordinator
COORDINATOR_NAMESPACE=default \
COORDINATOR_EXECUTOR_IMAGE=executor-demo/executor:demo \
./coordinator
```

Windows PowerShell：

```powershell
go build -o coordinator.exe ./cmd/coordinator
$env:COORDINATOR_NAMESPACE    = "default"
$env:COORDINATOR_EXECUTOR_IMAGE = "executor-demo/executor:demo"
./coordinator.exe
```

如需启用真实 IPFS，请额外设置 `COORDINATOR_IPFS_ENDPOINT`，否则默认回退到 `COORDINATOR_IPFS_MIRROR` 指定的本地目录。

### 环境变量

| 变量 | 说明 | 默认值 |
| --- | --- | --- |
| `COORDINATOR_NAMESPACE` | 创建 Job/ConfigMap 的命名空间 | `default` |
| `COORDINATOR_EXECUTOR_IMAGE` | Kubernetes Job 使用的执行器镜像 | `executor-demo/executor:demo` |
| `COORDINATOR_IPFS_MIRROR` | 占位 IPFS 客户端要读取的本地目录 | `./host/wasm` |
| `COORDINATOR_IPFS_ENDPOINT` | IPFS HTTP Gateway 地址，例如 `http://127.0.0.1:18480/ipfs` | 空（禁用） |
| `COORDINATOR_JOB_TEMPLATE` | Job 模板路径 | `k8s/job.yaml` |

### 任务流程
1. **拉取任务**：Contract 适配器依次返回 add/fib/affine 等样例任务。
2. **获取模块**：IPFS 适配器依据 `WasmCID` 从 Gateway 或本地目录读取 `.wasm` 内容。
3. **生成资源**：KubeManager 创建 ConfigMap，把 `module.wasm` 与 `input.json` 注入到模板中引用的卷。
4. **执行 Job**：Executor Pod 运行 Wasm，读取 `ENTRY` 或 `INPUT_PATH` 提供的参数，并在 `/mnt/shared/result.json` 输出结构化结果。
5. **收集清理**：Coordinator 读取 Pod 日志、提取 JSON 尾行，回写到任务来源，并删除 Job/ConfigMap。

### 示例任务
确保 `host/wasm` 中存在示例 `.wasm` 文件：

| 任务 | Wasm 文件 | 输入示例 | 说明 |
| --- | --- | --- | --- |
| `fib` | `fib.wasm` | `{"entry":"fib","args":[12]}` | 计算 `fib(12)=144` |
| `affine` | `affine.wasm` | `{"entry":"affine","args":[13,9,2]}` | 线性同余示例 |

Coordinator 会自动生成 ConfigMap，将输入写入 `/mnt/input/input.json`，Executor 则把输出写到 `/mnt/shared/result.json`。

### 本地 IPFS 服务示例
1. 启动仓库自带的 IPFS 节点（使用非常用端口 18480/18501 避免冲突）：
   ```bash
   docker compose -f ipfs/docker-compose.yml up -d
   ```
   Gateway 暴露在 `127.0.0.1:18480`，API 在 `127.0.0.1:18501`，容器会把仓库的 `host/wasm` 挂载到 `/wasm`。
2. 把 Wasm 文件添加到节点以获得 CID：
   ```bash
   docker exec wasm-exec-demo-ipfs ipfs add -Q /wasm/fib.wasm
   docker exec wasm-exec-demo-ipfs ipfs add -Q /wasm/affine.wasm
   ```
   请把输出的 CID 替换到 `internal/adapters/contract/placeholder.go` 或真实任务源里的 `WasmCID` 字段。
3. 运行 Coordinator 前设置网关：
   ```bash
   export COORDINATOR_IPFS_ENDPOINT=http://127.0.0.1:18480/ipfs
   ```
   PowerShell 等效命令：`$env:COORDINATOR_IPFS_ENDPOINT = "http://127.0.0.1:18480/ipfs"`
4. 如需停止节点：`docker compose -f ipfs/docker-compose.yml down`。

### 特色能力
- 顺序任务调度，默认一次只处理一个任务，方便调试与日志对齐。
- ConfigMap 注入 Wasm 和输入，便于追踪与复现。
- 统一的结果输出格式（文件 + 日志尾行 JSON），上层易于解析。
- 自动清理由 `DeleteArtifacts` 完成，避免 K8s 资源泄漏。
- 合约、IPFS 等适配器均为接口，可替换为真实链上 RPC 或对象存储实现。
- 提供 `scripts/run-docker.cmd SCENARIO=add|fib|affine` 方便本地端到端验证。

---

## Executor 模块

### 功能摘要
- 纯 Go + wazero + WASI，无需外部 C 依赖，可以嵌入任意镜像。
- 自动选择入口函数：优先使用 `ENTRY`，否则回退到 `add(uint64,uint64)` 并读取 `ADD_X/ADD_Y`。
- 支持从 `INPUT_PATH` 或 `ARGS_JSON` 读取结构化参数。
- 把结果写入 `/mnt/shared/result.txt` 或 `result.json`，并把最后一行设为原始结果便于 Coordinator 解析。
- 提供 `TIMEOUT_SEC` 限制，容器内默认使用非 root 用户。

### 环境变量

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `WASM_PATH` | `/mnt/wasm/module.wasm` | Wasm 模块路径 |
| `OUTPUT_PATH` | `/mnt/shared/result.txt` | 输出文件路径 |
| `ENTRY` | `run` | 首选入口函数（无参数） |
| `ADD_X` / `ADD_Y` | `5` / `7` | 回退 `add` 函数的参数 |
| `TIMEOUT_SEC` | `20` | 执行超时（秒） |
| `INPUT_PATH` / `ARGS_JSON` | 空 | JSON 输入（Coordinator 会注入） |

### 本地 Docker 运行
1. 准备宿主目录：
   ```bash
   mkdir -p host/wasm host/shared
   # 拷贝 module.wasm 到 host/wasm/module.wasm
   ```
2. 启动容器：
   - CMD：`scripts\run-docker.cmd`
   - PowerShell：`./scripts/run-docker.ps1`
   两个脚本默认 `docker build`，可通过 PowerShell `-SkipBuild` 或 CMD `set SKIP_BUILD=1` 跳过。
3. 查看结果：容器日志最后一行是原始结果，`host/shared/result.txt`（或 JSON）包含详细信息。

### Kubernetes 演示
> 适合 kind/minikube 等本地集群，生产环境请使用 PVC/对象存储等更可靠的卷。
1. 将 `k8s/job.yaml`、`k8s/pod.yaml` 里的 `hostPath.path` 改为 `host/` 目录的绝对路径。
2. 执行 `make k8s-apply` 并使用 `./scripts/tail-k8s.sh` 查看日志。
3. 通过 `kubectl logs job/wasm-executor-job` 或检查 `host/shared/result.txt` 获取结果。

### 直接在宿主机运行

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

### TinyGo 示例

```bash
cd /workspace/wasm-exec-demo/
docker exec -it Ubuntu-local bash
tinygo build -o host/wasm/fib.wasm -target=wasi ./examples/wasm-tinygo/fib
tinygo build -o host/wasm/affine.wasm -target=wasi ./examples/wasm-tinygo/affine
```

---

## 目录结构

```
.
|-- cmd/
|   |-- coordinator/
|   |   `-- main.go
|   `-- executor/
|       `-- main.go
|-- docs/
|   `-- COORDINATOR.md
|-- host/
|   |-- wasm/
|   `-- shared/
|-- ipfs/
|   `-- docker-compose.yml
|-- internal/
|   |-- adapters/
|   |   |-- contract/
|   |   `-- ipfs/
|   `-- coordinator/
|-- k8s/
|   |-- job.yaml
|   `-- pod.yaml
|-- scripts/
|   |-- run-docker.sh / .ps1 / .cmd
|   |-- run-k8s.sh
|   `-- tail-k8s.sh
`-- README.md
```

---

## 开发提示
- `go build ./...` 可快速验证 Coordinator 与 Executor 的编译状态。
- `scripts/run-docker.cmd SCENARIO=add|fib|affine` 能端到端复现占位任务并在 `host/shared/result.json` 检查输出。
- 建议在 `k8s/job.yaml` 中设置资源限制：
  ```yaml
  resources:
    limits:
      cpu: "500m"
      memory: "512Mi"
  ```

---

## 许可

MIT License © 2025
