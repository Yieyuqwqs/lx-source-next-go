# lx-source-next 常用命令
# 用法：make help

BIN     := lxsrcs
CONF    ?= data
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w

.PHONY: help build run test race fmt vet smoke cover clean check

help: ## 显示帮助
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-8s\033[0m %s\n", $$1, $$2}'

build: ## 编译
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/lxsrcs

run: ## 启动服务
	go run ./cmd/lxsrcs -conf $(CONF)

test: ## 跑单元测试
	go test ./...

race: ## 跑竞态检测
	go test ./... -race

fmt: ## 检查格式
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "未格式化："; echo "$$out"; exit 1; fi
	@echo "格式检查通过"

vet: ## 静态检查
	go vet ./...

smoke: ## 端到端冒烟（真起服务 + curl）
	bash scripts/smoke.sh

cover: ## 生成覆盖率报告
	go test ./... -covermode=atomic -coverprofile=coverage.out
	go tool cover -func=coverage.out | tail -1

check: fmt vet race ## 提交前全套检查

clean: ## 清理产物
	rm -f $(BIN) $(BIN).exe lxsrcs-smoke lxsrcs-smoke.exe coverage.out
	rm -rf data/cache data/log
