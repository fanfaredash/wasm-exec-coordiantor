# 中间层协调器（简化版）
该组件常驻在 Kubernetes 集群中，负责监听链上/占位任务、下载 Wasm 模块、投递 Job，并解析结果后回写上游。

## 编译与运行

Linux / macOS：
```bash
go build -o coordinator ./cmd/coordinator
COORDINATOR_NAMESPACE=default \
COORDINATOR_EXECUTOR_IMAGE=executor-demo/executor:demo \
COORDINATOR_IPFS_MIRROR=./host/wasm \
./coordinator
```

Windows PowerShell：
```powershell
go build -o coordinator ./cmd/coordinator.exe
$env:COORDINATOR_NAMESPACE = "default"
$env:COORDINATOR_EXECUTOR_IMAGE = "executor-demo/executor:demo"
$env:COORDINATOR_IPFS_MIRROR = ".\host\wasm"
.\coordinator
```

可用环境变量：
| 变量 | 说明 | 默认值 |
| --- | --- | --- |
| `COORDINATOR_NAMESPACE` | Job/ConfigMap 所在命名空间 | `default` |
| `COORDINATOR_EXECUTOR_IMAGE` | 执行器镜像（K8s Job 使用） | `executor-demo/executor:demo` |
| `COORDINATOR_IPFS_MIRROR` | 占位 IPFS 客户端读取 Wasm 的目录 | `./host/wasm` |
| `COORDINATOR_JOB_TEMPLATE` | Job 模板路径 | `k8s/job.yaml` |

## 工作流程与代码位置

1. **任务来源**（`internal/adapters/contract/placeholder.go`）
   - 占位合约客户端自动推送 `add / fib / affine` 三个示例任务，字段包含 `TaskID`、`WasmCID`、`Entry`、`InputJSON` 等。
2. **拉取模块**（`internal/adapters/ipfs/placeholder.go`）
   - 按 `WasmCID` 从 `COORDINATOR_IPFS_MIRROR` 读取对应的 Wasm 文件。
3. **调度 Job**（`internal/coordinator/k8s_manager.go` + `k8s_helpers.go`）
   - `CreateJob` 为任务创建两个 ConfigMap：`module.wasm` 与可选的 `input.json`。
   - `buildJobSpec` 根据模板注入 `ENTRY`、`INPUT_PATH` 等环境变量，挂载 ConfigMap 卷，并设置执行器镜像。
4. **执行器运行**（`cmd/executor/main.go`）
   - Job Pod 内的执行器读取 `WASM_PATH`、`ENTRY`、`INPUT_PATH/ARGS_JSON`，调用 Wasm 导出函数，并把结果写入 `/mnt/shared/result.json` 与 stdout。
5. **结果解析**（`internal/coordinator/coordinator.go`）
   - `FetchJobLogs` 获取 Pod 日志，`extractOutputValue` 解析末行 JSON，随后通过合约客户端回写结果并调用 `DeleteArtifacts` 清理 Job/ConfigMap。

> **模板参考**：`k8s/job.yaml`，展示了宿主目录挂载（`/mnt/wasm`、`/mnt/shared`）以及默认的环境变量占位。

## 示例任务

占位合约会顺序触发以下任务，确保 `host/wasm` 目录存在对应文件：

| 场景 | Wasm 文件 | 输入 | 说明 |
| --- | --- | --- | --- |
| `add` | `module.wasm` | `ENTRY=add`，`ADD_X/ADD_Y` 环境变量 | 传统加法示例 |
| `fib` | `fib.wasm` | `InputJSON = {"entry":"fib","args":[12]}` | 计算 `fib(12)=144` |
| `affine` | `affine.wasm` | `InputJSON = {"entry":"affine","args":[13,9,2]}` | 计算线性函数 |

协调器会为 `fib/affine` 自动注入输入 ConfigMap 并把 `INPUT_PATH` 指向 `/mnt/input/input.json`，无需手动写共享卷。

## 设计要点

- **顺序执行**：`internal/coordinator/coordinator.go` 串行拉取任务，易于追踪。
- **ConfigMap 注入**：`internal/coordinator/k8s_helpers.go` 负责把 `module.wasm`、`input.json` 变为卷并挂载到 Pod。
- **统一输出**：执行器始终写入 `/mnt/shared/result.json` 并输出 JSON 日志，`extractOutputValue` 只需读取末行。
- **即时清理**：任务完成后 `DeleteArtifacts` 会删除 Job 与 ConfigMap，避免残留。
- **可插拔**：`internal/adapters/contract` 与 `internal/adapters/ipfs` 通过接口抽象，可替换为真实链/存储实现。

## 构建 / 测试

- **本地构建**：`go build ./...` —— 拆分后的 Kube 管理代码已通过该命令验证。
- **示例验证**：参考 `scripts/run-docker.cmd`，设置 `SCENARIO=add|fib|affine` 并运行脚本，可在 docker 环境中触发对应任务，产物位于 `host/shared/result.json`。
