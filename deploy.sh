#!/bin/bash
set -e

echo "=========================================="
echo "  Octopus 部署脚本"
echo "=========================================="

echo ""
echo ">>> [1/5] 拉取最新代码"
git pull aks-fork dev

echo ""
echo ">>> [2/5] 构建前端 (Node.js 容器,复用 node_modules 缓存)"
# 获取 git 版本号。发布构建去掉 -dirty 后缀,使当前版本与 Release tag 一致,
# 避免前端 Info 页 "latestVersion !== backendNowVersion" 误报"有更新"。
GIT_VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_VERSION="${GIT_VERSION%%-dirty}"
echo "    版本号: $GIT_VERSION"
cd web
docker run --rm -v "$PWD":/w -w /w \
  -v "octopus-pnpm-store:/root/.local/share/pnpm/store" \
  -v "octopus-node-modules:/w/node_modules" \
  -e NEXT_PUBLIC_APP_VERSION="$GIT_VERSION" \
  node:22-alpine sh -c "
  corepack enable >/dev/null 2>&1
  pnpm install --frozen-lockfile
  pnpm run build
"
cd ..

echo ""
echo ">>> [3/5] 拷贝前端产物供 Go embed"
rm -rf static/out
mkdir -p static/out
cp -r web/out/* static/out/

echo ""
echo ">>> [4/5] 构建后端 (Golang 容器,复用 go module 缓存)"
mkdir -p build/docker/linux/amd64
# 注入版本号、commit、构建时间
GIT_COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME=$(date -u '+%Y-%m-%d %H:%M:%S UTC')
docker run --rm -v "$PWD":/src -w /src \
  -v "octopus-go-mod:/go/pkg/mod" \
  -v "octopus-go-build-cache:/root/.cache/go-build" \
  golang:1.25 sh -c "
  CGO_ENABLED=0 go build \
    -tags=jsoniter \
    -buildvcs=false \
    -ldflags='-s -w -X \"github.com/bestruirui/octopus/internal/conf.Version=$GIT_VERSION\" -X \"github.com/bestruirui/octopus/internal/conf.Commit=$GIT_COMMIT\" -X \"github.com/bestruirui/octopus/internal/conf.BuildTime=$BUILD_TIME\"' \
    -o build/docker/linux/amd64/octopus \
    .
"

echo ""
echo ">>> [5/5] 重新构建并启动容器"
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
