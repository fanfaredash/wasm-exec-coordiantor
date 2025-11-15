package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"executor/internal/adapters/contract"
	"executor/internal/adapters/ipfs"
	"executor/internal/coordinator"
)

// main 将配置 Config、适配器 Adapter 与协调器 Coordinator 事件循环串联起来。
func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	logger := log.New(os.Stdout, "", log.LstdFlags)
	var logAdapter coordinator.Logger = coordinatorStdLogger{logger: logger}

	cfg := coordinator.Config{
		Namespace:     envOr("COORDINATOR_NAMESPACE", "default"),
		ExecutorImage: envOr("COORDINATOR_EXECUTOR_IMAGE", "executor-demo/executor:demo"),
		JobTemplate:   envOr("COORDINATOR_JOB_TEMPLATE", "k8s/job.yaml"),
	}
	cfg.Log = logAdapter

	kube, err := coordinator.NewKubeManager(cfg.Namespace, cfg.Log)
	if err != nil {
		logger.Fatalf("kube manager: %v", err)
	}

	ipfsEndpoint := envOr("COORDINATOR_IPFS_ENDPOINT", "")
	var ipfsClient coordinator.IPFSClient
	if ipfsEndpoint != "" {
		client, err := ipfs.NewGatewayClient(ipfsEndpoint, cfg.Log)
		if err != nil {
			logger.Fatalf("ipfs gateway client: %v", err)
		}
		ipfsClient = client
		logger.Printf("[INFO] using ipfs gateway %s", ipfsEndpoint)
	} else {
		moduleDir := envOr("COORDINATOR_IPFS_MIRROR", filepath.Join("host", "wasm"))
		ipfsClient = ipfs.NewPlaceholderClient(moduleDir, cfg.Log)
		logger.Printf("[INFO] using local wasm mirror %s", moduleDir)
	}
	contractClient := contract.NewPlaceholderClient(cfg.Log)

	service, err := coordinator.NewCoordinator(cfg, contractClient, ipfsClient, kube)
	if err != nil {
		logger.Fatalf("coordinator: %v", err)
	}

	if err := service.Run(ctx); err != nil && err != context.Canceled {
		logger.Fatalf("run: %v", err)
	}
}

type coordinatorStdLogger struct {
	logger *log.Logger
}

// Infof 使用标准日志器输出协调器的普通信息。
func (l coordinatorStdLogger) Infof(format string, args ...any) {
	l.logger.Printf("[INFO] "+format, args...)
}

// Warnf 输出协调器处理过程中产生的警告。
func (l coordinatorStdLogger) Warnf(format string, args ...any) {
	l.logger.Printf("[WARN] "+format, args...)
}

// Errorf 输出错误日志，方便在宿主环境中排查失败原因。
func (l coordinatorStdLogger) Errorf(format string, args ...any) {
	l.logger.Printf("[ERROR] "+format, args...)
}

// envOr 读取环境变量，当变量不存在时返回默认值。
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
