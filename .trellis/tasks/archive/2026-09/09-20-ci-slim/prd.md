# CI 精简:只构 linux/amd64 + Alpine(其余注释保留)

## Goal

单人使用场景下 CI 过度构建(11 个二进制 + 2 套镜像 × 4 平台,QEMU 模拟占大头),发布一次要三四十分钟。**只保留 linux/amd64 二进制与 Alpine 单平台镜像**,其余**整段注释保留**(不删除),以后需要时取消注释即可恢复。

## Background(2026-09-20 用户提出,已核实)

- `scripts/build.sh`:`STANDARD_TARGETS` 7 项(linux amd64/arm64/arm/386、windows amd64、darwin arm64/amd64)+ `ANDROID_TARGETS` 4 项(NDK cgo);脚本顶部 `: "${ANDROID_NDK_HOME:?}"` 是**硬性要求**,注释 Android 时必须同时处理,否则脚本直接退出。
- `.github/workflows/release.yaml`:`Setup Android NDK`(nttld/setup-ndk,下载约 1GB)+ `Build`(bash scripts/build.sh)+ 两套 `docker/build-push-action`(Alpine/Debian)× `platforms: linux/amd64,linux/i386,linux/arm64,linux/arm/v7` + `setup-qemu-action`(非 amd64 全靠模拟,为主要耗时)。
- 用户部署:`docker-compose.yml` 固定 `TARGETPLATFORM: linux/amd64`,镜像从 Docker Hub 拉取;不使用 Release 里的 Windows/macOS/Android 二进制。

## Requirements

- R1 **二进制矩阵**:`STANDARD_TARGETS` 只启用 `linux:amd64`;其余 6 项**注释保留**(逐行注释或整段注释并说明如何恢复)。
- R2 **Android**:`ANDROID_TARGETS` 与相关构建调用、`ANDROID_NDK_HOME` 硬性校验**一并注释保留**;workflow 的 `Setup Android NDK` 步骤注释保留。
- R3 **Docker**:两套镜像构建均只保留 `platforms: linux/amd64`;`i386/arm64/arm/v7` 注释保留;`setup-qemu-action` 注释保留(amd64 单平台不需要);Debian 那套整段注释保留(保留 Alpine)。
- R4 **注释风格**:每处注释都写明「为什么注释」+「如何恢复」(取消注释即可),让未来的自己一眼看懂。
- R5 **可用性**:注释后 `scripts/build.sh` 必须能**在无 NDK 环境**下跑通,只产出 linux/amd64 二进制与其归档;workflow YAML 必须合法(可被解析),步骤顺序与依赖仍成立(如 Docker meta 残留引用需一并处理)。
- R6 不改版本号注入、发布/上传 Release、changelog、push 镜像等既有行为;`docker-compose.yml` 不动。

## Non-goals

- 不删除任何构建能力(只注释)。
- 不改 Dockerfile 本体。
- 不做 CI 缓存/并行优化(本次只做减法)。

## Acceptance Criteria

- [ ] AC1 本地以容器(golang:1.26,不设 ANDROID_NDK_HOME)跑 `bash scripts/build.sh` 成功,产物只有 linux/amd64 二进制 + 对应 zip 与 SHA256SUMS
- [ ] AC2 `release.yaml` 可被 YAML 解析(python yaml.safe_load)且动作步骤自洽(引用的 step id/输出仍存在)
- [ ] AC3 被注释项逐条可恢复:注释内保留原始行与最小恢复说明
- [ ] AC4 出一次发布验证(或推送一次分支构建)确认 CI 时长显著下降且镜像可用

## Notes

- 预计效果:去掉 NDK 下载、4 个 cgo 构建、3 个非 Linux 二进制、6 个 QEMU 镜像层 → CI 由数十分钟降到数分钟。
- 验证方式:build.sh 可在容器内真实跑;workflow 本身只能静态校验 + 一次真实推送观察(后者由用户决定何时执行)。
