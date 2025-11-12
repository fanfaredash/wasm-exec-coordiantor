package coordinator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Coordinator 负责串联链上事件、IPFS 拉取以及 Kubernetes 调度。
type Coordinator struct {
	cfg      Config
	contract ContractClient
	ipfs     IPFSClient
	kube     *KubeManager
	log      Logger
}

// NewCoordinator 使用外部依赖构建协调器实例。
func NewCoordinator(cfg Config, contract ContractClient, ipfs IPFSClient, kube *KubeManager) (*Coordinator, error) {
	if contract == nil {
		return nil, errors.New("contract client required")
	}
	if ipfs == nil {
		return nil, errors.New("ipfs client required")
	}
	if kube == nil {
		return nil, errors.New("kubernetes manager required")
	}
	cfg.applyDefaults()
	log := defaultLogger(cfg.Log)
	if err := kube.LoadTemplate(cfg.JobTemplate); err != nil {
		return nil, err
	}
	return &Coordinator{
		cfg:      cfg,
		contract: contract,
		ipfs:     ipfs,
		kube:     kube,
		log:      log,
	}, nil
}

// Run 持续运行直至上下文取消，驱动任务执行与清理。
func (c *Coordinator) Run(ctx context.Context) error {
	taskCh := make(chan TaskRequest)
	errCh := make(chan error, 1)

	go func() {
		errCh <- c.contract.SubscribeTasks(ctx, taskCh)
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errCh:
			if err != nil && !errors.Is(err, context.Canceled) {
				c.log.Errorf("task subscription failed: %v", err)
				return err
			}
			return nil
		case task := <-taskCh:
			c.processTask(ctx, task)
		}
	}
}

// processTask 负责单个计算任务的完整生命周期，从拉取输入到发布结果。
func (c *Coordinator) processTask(parent context.Context, task TaskRequest) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	c.log.Infof("processing task %s (cid=%s)", task.TaskID, task.WasmCID)

	if err := c.contract.AckTask(ctx, task.TaskID); err != nil {
		c.log.Warnf("ack task %s: %v", task.TaskID, err)
	}

	module, err := c.ipfs.FetchModule(ctx, task.WasmCID)
	if err != nil {
		c.log.Errorf("fetch module for %s: %v", task.TaskID, err)
		c.publishFailure(ctx, task, fmt.Errorf("fetch module: %w", err))
		return
	}

	jobName, configMaps, err := c.kube.CreateJob(ctx, c.cfg, task, module)
	if err != nil {
		c.log.Errorf("create job for %s: %v", task.TaskID, err)
		c.publishFailure(ctx, task, fmt.Errorf("create job: %w", err))
		return
	}

	defer c.kube.DeleteArtifacts(context.Background(), jobName, configMaps...)

	job, err := c.kube.WaitForJob(ctx, jobName)
	if err != nil {
		c.log.Errorf("wait job %s: %v", jobName, err)
		c.publishFailure(ctx, task, fmt.Errorf("wait job: %w", err))
		return
	}

	logs, err := c.kube.FetchJobLogs(ctx, jobName)
	if err != nil {
		c.log.Warnf("fetch logs %s: %v", jobName, err)
	}

	result := TaskResult{
		TaskID:     task.TaskID,
		Success:    job.Status.Succeeded > 0,
		Logs:       logs,
		FinishedAt: time.Now(),
		Metadata:   task.ResultMetadata,
	}

	if job.Status.Succeeded == 0 {
		if len(job.Status.Conditions) > 0 {
			result.Error = fmt.Errorf("job failed: %s", job.Status.Conditions[0].Message)
		} else {
			result.Error = fmt.Errorf("job failed without condition")
		}
	} else {
		result.OutputValue = extractOutputValue(logs)
	}

	if err := c.contract.PublishResult(ctx, result); err != nil {
		c.log.Errorf("publish result %s: %v", task.TaskID, err)
	}
}

// publishFailure 在任务失败时向合约层上报错误结果。
func (c *Coordinator) publishFailure(ctx context.Context, task TaskRequest, err error) {
	res := TaskResult{
		TaskID:     task.TaskID,
		Success:    false,
		Error:      err,
		FinishedAt: time.Now(),
		Metadata:   task.ResultMetadata,
	}
	if pubErr := c.contract.PublishResult(ctx, res); pubErr != nil {
		c.log.Errorf("publish failure %s: %v", task.TaskID, pubErr)
	}
}

// extractOutputValue 从 Job 日志末尾筛选最后一条非空行，作为原始结果值。
func extractOutputValue(logs string) string {
	lines := strings.Split(logs, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		return line
	}
	return ""
}
