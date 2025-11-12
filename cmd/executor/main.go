package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

type executorConfig struct {
	wasmPath   string
	outputPath string
	entry      string
	inputPath  string
	argsJSON   string
}

type inputSpec struct {
	Entry string   `json:"entry"`
	Args  []uint64 `json:"args"`
}

type execOutput struct {
	Entry   string   `json:"entry"`
	Args    []uint64 `json:"args"`
	Results []uint64 `json:"results"`
}

func getenvOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func mustUint64(val, name string) uint64 {
	u, err := strconv.ParseUint(val, 10, 64)
	if err != nil {
		log.Fatalf("invalid %s=%q: %v", name, val, err)
	}
	return u
}

func main() {
	cfg := loadConfig()
	wasmBin, err := os.ReadFile(cfg.wasmPath)
	if err != nil {
		log.Fatalf("read wasm from %s: %v", cfg.wasmPath, err)
	}

	ctx := context.Background()
	rt := wazero.NewRuntime(ctx)
	defer rt.Close(ctx)
	wasi_snapshot_preview1.MustInstantiate(ctx, rt)

	mod, err := rt.InstantiateWithConfig(ctx, wasmBin, wazero.NewModuleConfig().WithStartFunctions("_initialize"))
	if err != nil {
		log.Fatalf("instantiate wasm: %v", err)
	}

	entry, args, err := resolveInvocation(cfg)
	if err != nil {
		log.Fatalf("resolve invocation: %v", err)
	}
	fn := mod.ExportedFunction(entry)
	if fn == nil {
		log.Fatalf("exported function %q not found", entry)
	}

	results, err := fn.Call(ctx, args...)
	if err != nil {
		log.Fatalf("call %s failed: %v", entry, err)
	}

	output := execOutput{Entry: entry, Args: cloneSlice(args), Results: cloneSlice(results)}
	if err := writeOutput(cfg.outputPath, output); err != nil {
		log.Fatalf("write output: %v", err)
	}
	log.Printf("entry=%s args=%v results=%v", entry, args, results)
}

func loadConfig() executorConfig {
	return executorConfig{
		wasmPath:   getenvOr("WASM_PATH", "host/wasm/module.wasm"),
		outputPath: getenvOr("OUTPUT_PATH", "host/shared/result.txt"),
		entry:      getenvOr("ENTRY", "add"),
		inputPath:  getenvOr("INPUT_PATH", "/mnt/shared/input.json"),
		argsJSON:   strings.TrimSpace(os.Getenv("ARGS_JSON")),
	}
}

func resolveInvocation(cfg executorConfig) (string, []uint64, error) {
	spec, err := readInputSpec(cfg.inputPath)
	if err != nil {
		return "", nil, err
	}
	entry := cfg.entry
	if spec.Entry != "" {
		entry = spec.Entry
	}
	args := append([]uint64{}, spec.Args...)
	if len(args) == 0 && cfg.argsJSON != "" {
		if err := json.Unmarshal([]byte(cfg.argsJSON), &args); err != nil {
			return "", nil, fmt.Errorf("parse ARGS_JSON: %w", err)
		}
	}
	if len(args) == 0 {
		args = sequentialArgs()
	}
	if len(args) == 0 {
		args = legacyAddArgs()
	}
	return entry, args, nil
}

func readInputSpec(path string) (inputSpec, error) {
	var spec inputSpec
	if path == "" {
		return spec, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return spec, nil
		}
		return spec, fmt.Errorf("read %s: %w", path, err)
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return spec, nil
	}
	if err := json.Unmarshal([]byte(content), &spec); err != nil {
		return spec, fmt.Errorf("parse %s: %w", path, err)
	}
	return spec, nil
}

func sequentialArgs() []uint64 {
	var args []uint64
	for i := 0; ; i++ {
		candidates := []string{
			fmt.Sprintf("ARG_%d", i),
			fmt.Sprintf("ARG%d", i),
		}
		var (
			val  string
			name string
		)
		for _, c := range candidates {
			if v := strings.TrimSpace(os.Getenv(c)); v != "" {
				val = v
				name = c
				break
			}
		}
		if val == "" {
			break
		}
		args = append(args, mustUint64(val, name))
	}
	return args
}

func legacyAddArgs() []uint64 {
	x := strings.TrimSpace(os.Getenv("ADD_X"))
	y := strings.TrimSpace(os.Getenv("ADD_Y"))
	if x == "" || y == "" {
		return nil
	}
	return []uint64{mustUint64(x, "ADD_X"), mustUint64(y, "ADD_Y")}
}

func writeOutput(path string, out execOutput) error {
	if out.Args == nil {
		out.Args = []uint64{}
	}
	if out.Results == nil {
		out.Results = []uint64{}
	}
	payload, err := json.Marshal(out)
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := os.WriteFile(path, append(payload, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Println(string(payload))
	return nil
}

func cloneSlice(src []uint64) []uint64 {
	if len(src) == 0 {
		return []uint64{}
	}
	dup := make([]uint64, len(src))
	copy(dup, src)
	return dup
}
