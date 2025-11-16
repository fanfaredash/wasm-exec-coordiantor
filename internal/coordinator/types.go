package coordinator

import (
	"context"
	"time"
)

// TaskRequest 表示一次计算任务。
type TaskRequest struct {
	TaskID         string
	WasmCID        string
	InputCID       string
	Entry          string
	Args           map[string]string
	InputJSON      []byte
	ResultMetadata map[string]string
}

// TaskResult 描述任务执行结果。
type TaskResult struct {
	TaskID      string
	Success     bool
	OutputValue string
	Logs        string
	FinishedAt  time.Time
	Error       error
	Metadata    map[string]string
}

// ContractClient 抽象链上交互。
type ContractClient interface {
	SubscribeTasks(ctx context.Context, out chan<- TaskRequest) error
	AckTask(ctx context.Context, taskID string) error
	PublishResult(ctx context.Context, result TaskResult) error
}

// IPFSClient 抽象 Wasm 模块下载。
type IPFSClient interface {
	FetchModule(ctx context.Context, cid string) ([]byte, error)
}

// Logger 提供基础日志输出。
type Logger interface {
	Infof(format string, args ...any)
	Warnf(format string, args ...any)
	Errorf(format string, args ...any)
}
