package contract

import (
	"context"

	"executor/internal/coordinator"
)

// PlaceholderClient 是用于日志演示的合约客户端占位实现。
type PlaceholderClient struct {
	log coordinator.Logger
}

// NewPlaceholderClient 构造占位实现，方便本地调试。
func NewPlaceholderClient(log coordinator.Logger) *PlaceholderClient {
	return &PlaceholderClient{log: log}
}

// SubscribeTasks 依次投递 add/fib/affine 三个示例任务，随后阻塞等待取消。
func (p *PlaceholderClient) SubscribeTasks(ctx context.Context, out chan<- coordinator.TaskRequest) error {
	tasks := []coordinator.TaskRequest{
		{
			TaskID:  "demo-add-001",
			WasmCID: "module.wasm",
			Entry:   "add",
			Args: map[string]string{
				"ADD_X": "5",
				"ADD_Y": "7",
			},
			ResultMetadata: map[string]string{
				"description": "demo addition task emitted by placeholder client",
				"scenario":    "add",
			},
		},
		{
			TaskID:    "demo-fib-001",
			WasmCID:   "fib.wasm",
			Entry:     "fib",
			InputJSON: []byte(`{"entry":"fib","args":[12]}`),
			ResultMetadata: map[string]string{
				"description": "demo fibonacci task emitted by placeholder client",
				"scenario":    "fib",
			},
		},
		{
			TaskID:    "demo-affine-001",
			WasmCID:   "affine.wasm",
			Entry:     "affine",
			InputJSON: []byte(`{"entry":"affine","args":[13,9,2]}`),
			ResultMetadata: map[string]string{
				"description": "demo affine task emitted by placeholder client",
				"scenario":    "affine",
			},
		},
	}

	for _, task := range tasks {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case out <- task:
			p.log.Warnf("placeholder contract client emitted task %s (scenario=%s)", task.TaskID, task.ResultMetadata["scenario"])
		}
	}

	p.log.Warnf("placeholder contract client idle; awaiting cancellation")
	<-ctx.Done()
	return ctx.Err()
}

// AckTask 在日志中确认任务已被接收，便于追踪。
func (p *PlaceholderClient) AckTask(ctx context.Context, taskID string) error {
	p.log.Infof("ack task %s (placeholder)", taskID)
	return nil
}

// PublishResult 仅打印任务结果，不与真实合约交互。
func (p *PlaceholderClient) PublishResult(ctx context.Context, result coordinator.TaskResult) error {
	if result.Success {
		p.log.Infof("task %s succeeded, output=%s", result.TaskID, result.OutputValue)
	} else {
		p.log.Warnf("task %s failed: %v", result.TaskID, result.Error)
	}
	return nil
}
