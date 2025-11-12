# Go build stage：编译执行器二进制
FROM golang:1.25.2-alpine AS go-builder
WORKDIR /src
ENV CGO_ENABLED=0
COPY go.mod ./
RUN go mod download
COPY . .
RUN mkdir -p /out && go build -trimpath -ldflags="-s -w" -o /out/executor ./cmd/executor

# TinyGo stage：编译示例 Wasm 模块
FROM tinygo/tinygo:0.33.0 AS tinygo-builder
WORKDIR /src
COPY examples/wasm-tinygo ./examples/wasm-tinygo
RUN tinygo build -o module.wasm -target=wasi ./examples/wasm-tinygo/main.go

# final stage
FROM gcr.io/distroless/static:nonroot
COPY --from=go-builder /out/executor /executor
COPY --from=tinygo-builder /src/module.wasm /examples/module.wasm
USER 65532:65532
ENTRYPOINT ["/executor"]
