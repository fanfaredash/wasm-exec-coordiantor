# Executor - 基于 wazero 的 WebAssembly 执行器

该模块提供了一个 WebAssembly 执行器，用于在容器或 Kubernetes 集群中安全运行 Wasm 模块。它基于纯 Go 实现的 [wazero](https://github.com/tetratelabs/wazero) 运行时构建，因此无需依赖任何外部 C 库，并且可以嵌入到任意 Go 应用程序或容器镜像中。

---

## 功能特性

* 从挂载的目录中加载 `module.wasm` 文件
* 使用 **wazero + WASI** 实例化并执行模块
* 自动检测导出的入口函数：

  * 优先使用由环境变量 `ENTRY` 指定的函数名（默认为 `run`，无参数）
  * 若无则回退到 `add(uint64,uint64)`，参数来自环境变量 `ADD_X` 和 `ADD_Y`
* 将执行结果写入 `/mnt/shared/result.txt`
* 将结果打印到标准输出，其中最后一行仅包含原始数值，方便日志解析
* 支持可配置的执行超时时间

---

## 目录结构

```
executor/
|-- README.md
|-- go.mod
|-- Dockerfile
|-- cmd/
|   `-- executor/
|       `-- main.go
|-- k8s/
|   |-- job.yaml
|   `-- pod.yaml
|-- scripts/
|   |-- run-docker.sh
|   |-- run-k8s.sh
|   `-- tail-k8s.sh
`-- host/
    |-- wasm/      # 将 module.wasm 放在此处
    `-- shared/    # 执行结果（result.txt）会生成在此处
```

---

## 环境变量

| 变量名           | 默认值                      | 说明               |
| ------------- | ------------------------ | ---------------- |
| `WASM_PATH`   | `/mnt/wasm/module.wasm`  | Wasm 模块的路径       |
| `OUTPUT_PATH` | `/mnt/shared/result.txt` | 结果文件的输出路径        |
| `ENTRY`       | `run`                    | 要调用的主要导出函数（无参数）  |
| `ADD_X`       | `5`                      | `add` 回退函数的第一个参数 |
| `ADD_Y`       | `7`                      | `add` 回退函数的第二个参数 |
| `TIMEOUT_SEC` | `20`                     | 执行超时时间（单位：秒）     |

---

## 使用 Docker 本地运行

### 1. 准备宿主机目录

```bash
mkdir -p host/wasm host/shared
# 将你的 module 拷贝至 host/wasm/module.wasm
```

### 2. 启动容器

CMD：

```cmd
scripts\run-docker.cmd
```

PowerShell：

```powershell
./scripts/run-docker.ps1
```

> 两个脚本默认都会执行 `docker build`。
> 如果希望跳过镜像构建，可在 PowerShell 中传入 `-SkipBuild` 参数，或在 CMD 中先执行 `set SKIP_BUILD=1`。

### 3. 查看结果

* 容器日志的最后一行是原始结果数值
* `host/shared/result.txt` 文件中包含时间戳和结果信息

---

## 在 Kubernetes 中运行（hostPath 演示）

> 适用于本地演示集群（如 kind 或 minikube）。生产环境请使用 PVC 或对象存储。

### 1. 修改宿主机路径

编辑 `k8s/job.yaml` 和 `k8s/pod.yaml`，将其中的每个 `hostPath.path` 替换为仓库 `host/` 目录的绝对路径。

### 2. 部署并查看日志

```bash
make k8s-apply
./scripts/tail-k8s.sh
```

### 3. 查看执行结果

```bash
kubectl logs job/wasm-executor-job
# 或直接查看 host/shared/result.txt
```

---

## 安全与限制

* 在 WASI 沙箱中运行，不允许外部系统调用
* 容器进程以非 root 用户身份运行
* 推荐设置资源限制：

```yaml
resources:
  limits:
    cpu: "500m"
    memory: "512Mi"
```

* 将 `/mnt/wasm` 挂载为只读以保护输入模块

---

## 开发说明

### 构建 Go 二进制

```bash
go mod tidy
go build -o executor ./cmd/executor
```

### 直接在宿主机运行

```bash
WASM_PATH=./host/wasm/module.wasm OUTPUT_PATH=./host/shared/result.txt ./executor
```

---

## 辅助脚本

| 脚本名称                    | 功能说明                   |
| ----------------------- | ---------------------- |
| `scripts/run-docker.sh` | 在 Docker 中运行执行器并挂载宿主目录 |
| `scripts/run-k8s.sh`    | 创建 Kubernetes Job      |
| `scripts/tail-k8s.sh`   | 追踪 Job 日志并打印执行结果       |

---

## 输出示例

```
timestamp: 2025-10-24T08:33:45Z
result: 12
12
```

最后一行（`12`）用于下游系统（如中继器）解析。

---

## 集成提示

该执行器仅处理本地文件，不直接与 IPFS 或区块链交互。上层组件（例如中继器）可执行以下操作：

1. 将 Wasm 模块下载到挂载目录
2. 启动执行器容器
3. 将 `result.txt` 上传到 IPFS 或其他存储系统
4. 将生成的 CID 记录到链上

---

## 本机代码

Tinygo 编译需要进入虚拟机：

```bash
cd /workspace/wasm-exec-demo/
docker exec -it Ubuntu-local bash
tinygo build -o host/wasm/fib.wasm -target=wasi ./examples/wasm-tinygo/fib
tinygo build -o host/wasm/affine.wasm -target=wasi ./examples/wasm-tinygo/affine
```

## 直接运行

```
$env:WASM_PATH   = "host\wasm\fib.wasm"
$env:INPUT_PATH  = "host\shared\input.json"   # 或其他 JSON 描述路径
$env:OUTPUT_PATH = "host\shared\result.json"
$env:ENTRY       = "fib"                     # 改成模块里真正的导出名，其实应该在input里有输入
.\cmd\executor\executor.exe
```

## run-docker.cmd 运行示例
### Fibonacci demo

```
cd C:\Users\lexa\Desktop\CrossChain\wasm-exec-demo
set SCENARIO=fib
set SKIP_BUILD=1           # 可省略以便自动 docker build
scripts\run-docker.cmd
```
- 期望 host\wasm\fib.wasm；脚本会自动把 examples\wasm-tinygo\fib\input.json 复制到 host\shared\input.json，传入 ENTRY=fib，输出文件为 host\shared\result-fib.json，末行 JSON 形如 {"entry":"fib","args":[12],"results":[144]}。

### Affine demo

```
cmd
cd C:\Users\lexa\Desktop\CrossChain\wasm-exec-demo
set SCENARIO=affine
scripts\run-docker.cmd
```

- 使用 host\wasm\affine.wasm，输入拷贝自 examples\wasm-tinygo\affine\input.json（{"entry":"affine","args":[13,9,2]}），结果输出到 host\shared\result-affine.json。

---

## 许可证

MIT License © 2025

Coordinator service details: see docs/COORDINATOR.md
