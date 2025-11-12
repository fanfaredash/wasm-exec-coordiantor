#!/usr/bin/env bash
set -euo pipefail

# 根目录与路径
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
HOST="$ROOT/host"
WASM_OUT="$HOST/wasm/module.wasm"
RESULT_OUT="$HOST/shared/result.txt"

# 可选环境变量（默认值）
: "${ENTRY:=}"          # 留空则直接调用 add(x,y)
: "${ADD_X:=5}"
: "${ADD_Y:=7}"
: "${WASM_PATH:=$WASM_OUT}"
: "${OUTPUT_PATH:=$RESULT_OUT}"

# 1) 准备目录
mkdir -p "$HOST/wasm" "$HOST/shared"

# 2) 编译 Wasm 模块（examples/wasm-tinygo/main.go）
pushd "$ROOT/examples/wasm-tinygo" >/dev/null
tinygo build -o "$WASM_OUT" -target=wasi ./main.go
popd >/dev/null

# 3) 构建执行器二进制（cmd/executor/main.go）
pushd "$ROOT/cmd/executor" >/dev/null
go build -o "$ROOT/executor.bin" ./main.go
popd >/dev/null

# 4) 执行（先尝试 ENTRY，无则默认 add）
WASM_PATH="$WASM_PATH" \
OUTPUT_PATH="$OUTPUT_PATH" \
ENTRY="$ENTRY" \
ADD_X="$ADD_X" \
ADD_Y="$ADD_Y" \
"$ROOT/executor.bin"
