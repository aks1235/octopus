#!/bin/bash
set -e

echo "=========================================="
echo "  Octopus 部署脚本 (dev-v2)"
echo "=========================================="

echo ""
echo ">>> [1/4] 拉取最新代码"
git pull aks-fork dev-v2

echo ""
echo ">>> [2/4] 构建前端 (Node.js 容器,复用 node_modules 缓存)"
# 获取 git 版本号。发布构建去掉 -dirty 后缀,使当前版本与 Release tag 一致,
# 避免前端 Info 页 "latestVersion !== backendNowVersion" 误报"有更新"。
GIT_VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_VERSION="${GIT_VERSION%%-dirty}"
echo "    版本号: $GIT_VERSION"
# 挂载仓库根而非 web/ 子目录:vite outDir 以绝对路径解析到 ../static/out,
# 只挂子目录时产物会落到容器内 /static/out 随容器销毁(feat 线实踩)。
docker run --rm -v "$PWD":/src -w /src/web \
  -v "octopus-pnpm-store:/root/.local/share/pnpm/store" \
  -v "octopus-node-modules:/src/web/node_modules" \
  -e VITE_APP_VERSION="$GIT_VERSION" \
  node:22-alpine sh -c "
  corepack enable >/dev/null 2>&1
  pnpm install --frozen-lockfile
  pnpm run build
"

echo ""
echo ">>> [3/4] 构建后端 (Golang 容器,复用 go module 缓存)"
mkdir -p build/docker/linux/amd64
# 注入版本号、commit、构建时间、仓库指向(与 CI release.yaml 保持一致)
# -a 强制全量重编译:go build 缓存会误判 go:embed 未变化,导致 static/out 改动不进二进制(前端产物滞后),加 -a 绕过该问题
GIT_COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME=$(date -u '+%Y-%m-%d %H:%M:%S UTC')
# golang 版本对齐 go.mod 的 go 指令(1.26.4)
docker run --rm -v "$PWD":/src -w /src \
  -v "octopus-go-mod:/go/pkg/mod" \
  -v "octopus-go-build-cache:/root/.cache/go-build" \
  golang:1.26 sh -c "
  CGO_ENABLED=0 go build \
    -a \
    -tags=jsoniter \
    -buildvcs=false \
    -ldflags='-s -w -X \"github.com/bestruirui/octopus/internal/conf.Version=$GIT_VERSION\" -X \"github.com/bestruirui/octopus/internal/conf.Commit=$GIT_COMMIT\" -X \"github.com/bestruirui/octopus/internal/conf.BuildTime=$BUILD_TIME\" -X \"github.com/bestruirui/octopus/internal/conf.Author=aks1235\" -X \"github.com/bestruirui/octopus/internal/conf.Repo=https://github.com/aks1235/octopus\"' \
    -o build/docker/linux/amd64/octopus \
    .
"

echo ""
echo ">>> [4/4] 重新构建并启动容器"
docker compose down
docker compose build
docker compose up -d

echo ""
echo "=========================================="
echo "✓ 部署完成"
echo "=========================================="
echo ""
echo "容器状态:"
docker compose ps
echo ""
echo "访问地址: http://127.0.0.1:8080"
echo "查看日志: docker compose logs -f"
