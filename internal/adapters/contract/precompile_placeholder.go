package contract

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"golang.org/x/crypto/sha3"
)

const (
	defaultInstructionLimit = 1000
	defaultExecTimeout      = 5 * time.Second
)

var errInstructionLimit = errors.New("instruction limit exceeded")

// IPFSFetcher defines the subset needed for precompile-style verification.
type IPFSFetcher interface {
	FetchModule(ctx context.Context, cid string) ([]byte, error)
}

// MerkleProof captures the data required to prove a snapshot leaf exists under a root.
type MerkleProof struct {
	Leaf []byte
	Root []byte
	Path [][]byte
}

// VerifySnapshotPrecompile simulates the on-chain precompile:
// 1) validate Merkle inclusion of the snapshot hash against the provided root.
// 2) fetch the snapshot Wasm blob from IPFS.
// 3) execute it via wazero with a simple instruction counter (weight=1) capped at 1000.
// Returns true on successful verification+execution, false if Merkle fails or execution breaks limits.
func VerifySnapshotPrecompile(
	ctx context.Context,
	ipfs IPFSFetcher,
	snapshotCID string,
	proof MerkleProof,
) (bool, error) {
	if snapshotCID == "" {
		return false, errors.New("missing snapshot CID")
	}
	if len(proof.Leaf) == 0 || len(proof.Root) == 0 {
		return false, errors.New("missing proof data")
	}
	if !verifyMerkle(proof) {
		return false, nil
	}

	wasmBytes, err := ipfs.FetchModule(ctx, snapshotCID)
	if err != nil {
		return false, fmt.Errorf("fetch snapshot from ipfs: %w", err)
	}

	ok, err := executeWithInstructionBudget(ctx, wasmBytes, defaultInstructionLimit)
	return ok, err
}

// verifyMerkle follows the same sorting+keccak rule used in the Solidity contract.
func verifyMerkle(p MerkleProof) bool {
	computed := make([]byte, len(p.Leaf))
	copy(computed, p.Leaf)

	for _, sib := range p.Path {
		if bytes.Compare(computed, sib) < 0 {
			computed = keccakPair(computed, sib)
		} else {
			computed = keccakPair(sib, computed)
		}
	}
	return bytes.Equal(computed, p.Root)
}

func keccakPair(a, b []byte) []byte {
	h := sha3.NewLegacyKeccak256()
	h.Write(a)
	h.Write(b)
	return h.Sum(nil)
}

// executeWithInstructionBudget runs a wasm module under wazero and enforces a simple
// instruction count budget via a function listener. Each function call is weighted as 1
// instruction; this is a placeholder approximation for real opcode-level accounting.
func executeWithInstructionBudget(ctx context.Context, wasmBytes []byte, limit uint64) (bool, error) {
	var panicErr error

	meter := &instructionMeter{limit: limit}
	rtCfg := wazero.NewRuntimeConfig().
		WithCloseOnContextDone(true).
		WithFunctionListenerFactory(meter)

	rt := wazero.NewRuntimeWithConfig(ctx, rtCfg)
	defer rt.Close(ctx)

	// Enable WASI by default to support snapshots built with WASI ABI.
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		return false, fmt.Errorf("init wasi: %w", err)
	}

	execCtx, cancel := context.WithTimeout(ctx, defaultExecTimeout)
	defer cancel()

	defer func() {
		if r := recover(); r != nil {
			// Translate instruction limit panics to a boolean failure.
			cancel()
			if r == errInstructionLimit {
				panicErr = errInstructionLimit
				return
			}
			panicErr = fmt.Errorf("wasm panic: %v", r)
		}
	}()

	mod, err := rt.InstantiateModuleFromCode(execCtx, wasmBytes)
	if err != nil {
		return false, fmt.Errorf("instantiate wasm: %w", err)
	}
	defer mod.Close(execCtx)

	// Try calling _start if present; otherwise try "run" and ignore if neither exists.
	if fn := mod.ExportedFunction("_start"); fn != nil {
		if _, err := fn.Call(execCtx); err != nil {
			if errors.Is(err, errInstructionLimit) {
				return false, nil
			}
			return false, fmt.Errorf("execute _start: %w", err)
		}
	} else if fn := mod.ExportedFunction("run"); fn != nil {
		if _, err := fn.Call(execCtx); err != nil {
			if errors.Is(err, errInstructionLimit) {
				return false, nil
			}
			return false, fmt.Errorf("execute run: %w", err)
		}
	}

	if panicErr != nil {
		if errors.Is(panicErr, errInstructionLimit) {
			return false, nil
		}
		return false, panicErr
	}

	return true, nil
}

// instructionMeter counts function entries as a proxy for instruction weight.
// In a real precompile this should be replaced with opcode-level metering.
type instructionMeter struct {
	limit uint64
	count uint64
}

func (m *instructionMeter) NewListener(api.FunctionDefinition) experimental.FunctionListener {
	return m
}

func (m *instructionMeter) Before(ctx context.Context, _ api.Module, _ api.FunctionDefinition, _ []uint64) {
	m.count++
	if m.count > m.limit {
		panic(errInstructionLimit)
	}
}

func (*instructionMeter) After(ctx context.Context, _ api.Module, _ api.FunctionDefinition, _ []uint64, _ []uint64) {
}
