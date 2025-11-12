package ipfs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"executor/internal/coordinator"
)

// PlaceholderClient 从本地目录读取与 CID 对应的 Wasm 模块。
type PlaceholderClient struct {
	ModuleDir string
	log       coordinator.Logger
}

// NewPlaceholderClient 创建基于本地文件的 IPFS 客户端占位实现。
func NewPlaceholderClient(dir string, log coordinator.Logger) *PlaceholderClient {
	return &PlaceholderClient{
		ModuleDir: dir,
		log:       log,
	}
}

// FetchModule 从磁盘加载模块字节，替代真实 IPFS 拉取。
func (p *PlaceholderClient) FetchModule(ctx context.Context, cid string) ([]byte, error) {
	if p.ModuleDir == "" {
		return nil, fmt.Errorf("module directory not configured")
	}
	if cid == "" {
		return nil, fmt.Errorf("empty cid")
	}
	path := filepath.Join(p.ModuleDir, cid)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read module %s: %w", path, err)
	}
	p.log.Infof("loaded wasm module %s (%d bytes)", cid, len(data))
	return data, nil
}
