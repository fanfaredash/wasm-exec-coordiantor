package ipfs

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"executor/internal/coordinator"
)

const (
	defaultGatewayTimeout = 30 * time.Second
	maxModuleBytes        = 64 << 20 // 64MiB safety limit
)

// GatewayClient 通过 HTTP Gateway 拉取 Wasm 模块，兼容本地与远程 IPFS 服务。
type GatewayClient struct {
	baseURL string
	client  *http.Client
	log     coordinator.Logger
}

// NewGatewayClient 构造面向 HTTP Gateway 的 IPFS 客户端。
func NewGatewayClient(baseURL string, log coordinator.Logger) (*GatewayClient, error) {
	trimmed := strings.TrimSpace(baseURL)
	if trimmed == "" {
		return nil, fmt.Errorf("ipfs gateway base url is empty")
	}
	return &GatewayClient{
		baseURL: strings.TrimRight(trimmed, "/"),
		client: &http.Client{
			Timeout: defaultGatewayTimeout,
		},
		log: log,
	}, nil
}

// FetchModule 通过网关下载指定 CID 的字节流。
func (g *GatewayClient) FetchModule(ctx context.Context, cid string) ([]byte, error) {
	if cid == "" {
		return nil, fmt.Errorf("cid is empty")
	}
	target := fmt.Sprintf("%s/%s", g.baseURL, strings.TrimLeft(cid, "/"))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", target, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("gateway %s status %s: %s", target, resp.Status, strings.TrimSpace(string(payload)))
	}

	reader := io.LimitReader(resp.Body, maxModuleBytes+1)
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", target, err)
	}
	if len(data) > int(maxModuleBytes) {
		return nil, fmt.Errorf("module larger than %d bytes", maxModuleBytes)
	}

	g.log.Infof("downloaded wasm module %s (%d bytes) via ipfs gateway", cid, len(data))
	return data, nil
}
