#!/bin/bash
set -euo pipefail

readonly APP_NAME="octopus" # 发布产物和容器内的可执行文件名。
readonly OUTPUT_DIR="build" # 所有构建、归档和容器输入的根目录。
readonly VERSION="$(git describe --tags --abbrev=0 2>/dev/null || echo 'dev')" # 当前发布版本。
readonly COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo 'unknown')" # 当前提交短哈希。
readonly LDFLAGS="-X 'github.com/bestruirui/${APP_NAME}/internal/conf.Version=${VERSION}' \
                  -X 'github.com/bestruirui/${APP_NAME}/internal/conf.BuildTime=$(TZ='Asia/Shanghai' date +'%F %T %z')' \
                  -X 'github.com/bestruirui/${APP_NAME}/internal/conf.Author=aks1235' \
                  -X 'github.com/bestruirui/${APP_NAME}/internal/conf.Repo=https://github.com/aks1235/octopus' \
                  -X 'github.com/bestruirui/${APP_NAME}/internal/conf.Commit=${COMMIT}' \
                  -s -w" # 注入版本信息(含仓库指向)并缩小发布二进制。

build_standard() {
    # 标准矩阵直接使用 GOOS/GOARCH，arm 固定输出 ARMv7 指令集。
    local os go_arch
    IFS=: read -r os go_arch <<<"$1"
    local build_env=(GOOS="${os}" GOARCH="${go_arch}" CGO_ENABLED=0)
    if [ "${go_arch}" = "arm" ]; then
        build_env+=(GOARM=7)
    fi
    echo "Building ${os}/${go_arch}"
    env "${build_env[@]}" go build -trimpath -o "${OUTPUT_DIR}/bin/${APP_NAME}-${os}-${go_arch}" -ldflags="${LDFLAGS}" -tags=jsoniter .
}

build_android() {
    # 【当前未被调用】Android 构建已随 CI 精简停用(见下方 ANDROID_TARGETS 注释),
    # 函数体特意保留,恢复时无需重写。
    # Android 矩阵显式绑定 NDK API 21 编译器，脚本只面向 workflow 的 Ubuntu runner。
    local go_arch compiler
    IFS=: read -r go_arch compiler <<<"$1"
    local build_env=(GOOS=android GOARCH="${go_arch}" CGO_ENABLED=1 \
        CC="${ANDROID_NDK_HOME}/toolchains/llvm/prebuilt/linux-x86_64/bin/${compiler}")
    if [ "${go_arch}" = "arm" ]; then
        build_env+=(GOARM=7)
    fi
    echo "Building android/${go_arch}"
    env "${build_env[@]}" go build -trimpath -o "${OUTPUT_DIR}/bin/${APP_NAME}-android-${go_arch}" -ldflags="${LDFLAGS}" -tags=jsoniter .
}

# 【为什么只留一项】单人部署只用 linux/amd64 二进制(docker-compose 固定 TARGETPLATFORM=linux/amd64,
# 镜像从 Docker Hub 拉取),其余 6 项属纯冗余构建耗时,故注释停用(2026-09-20 CI 精简)。
# 【如何恢复】取消下面被注释的 6 行即可,无需改动其他代码。
readonly -a STANDARD_TARGETS=(
    "linux:amd64"
    # "linux:arm64"
    # "linux:arm"
    # "linux:386"
    # "windows:amd64"
    # "darwin:arm64"
    # "darwin:amd64"
) # 不依赖 cgo 的固定发布矩阵。

# 【为什么整段注释】Android 目标需 cgo + NDK(r28c,约 1GB 下载),本项目不使用 Android 产物。
# 【如何恢复】① 取消下面 ANDROID_TARGETS 声明注释 ② 取消下方 ANDROID_NDK_HOME 校验注释
#            ③ 取消文件末尾的 ANDROID_TARGETS 构建循环注释
#            ④ workflow 侧同时取消 "Setup Android NDK" 步骤与 Build 步骤的 ANDROID_NDK_HOME env 注释。
# readonly -a ANDROID_TARGETS=(
#     "amd64:x86_64-linux-android21-clang"
#     "arm64:aarch64-linux-android21-clang"
#     "arm:armv7a-linux-androideabi21-clang"
#     "386:i686-linux-android21-clang"
# ) # Android API 21 的固定 ABI 与 NDK clang 映射。

# 【为什么注释】这行原是硬性校验:无 ANDROID_NDK_HOME 时脚本直接退出,会使无 NDK 的本地/容器构建无法跑通。
# 【如何恢复】取消本行注释,并保证 workflow 已注入 ANDROID_NDK_HOME(见上面 Android 恢复步骤 ④)。
# : "${ANDROID_NDK_HOME:?ANDROID_NDK_HOME is required}"

# 构建工具不会创建父目录，因此只保留一次直接创建。
# 非 amd64 的目录保留为空目录(无产物、无副作用):这样恢复其他平台时只需取消下面的构建/cp 注释,
# 不必再改这里。若想彻底清干净,可把 386 / arm/v7 / arm64 三项一并注释。
mkdir -p "${OUTPUT_DIR}/bin" "${OUTPUT_DIR}/archives" \
    "${OUTPUT_DIR}/docker/linux/amd64" "${OUTPUT_DIR}/docker/linux/386" \
    "${OUTPUT_DIR}/docker/linux/arm/v7" "${OUTPUT_DIR}/docker/linux/arm64"
rm -f "${OUTPUT_DIR}"/bin/"${APP_NAME}"-* "${OUTPUT_DIR}"/archives/*.zip "${OUTPUT_DIR}/archives/SHA256SUMS"

echo "Building ${APP_NAME} ${VERSION} (${COMMIT})"
for target in "${STANDARD_TARGETS[@]}"; do
    build_standard "${target}"
done
# 【为什么注释】ANDROID_TARGETS 声明已注释,这里再引用在 set -u 下会报未绑定变量;
# 且 Android 构建依赖 NDK,已随 CI 精简一并停用。
# 【如何恢复】与上面 ANDROID_TARGETS 声明一起取消注释。
# for target in "${ANDROID_TARGETS[@]}"; do
#     build_android "${target}"
# done

# Docker buildx 按 TARGETPLATFORM 读取固定目录中的同名可执行文件。
cp "${OUTPUT_DIR}/bin/${APP_NAME}-linux-amd64" "${OUTPUT_DIR}/docker/linux/amd64/${APP_NAME}"
# 【为什么注释】下面三个平台的二进制已不构建,源文件不存在,set -e 下 cp 会直接让脚本失败。
# 【如何恢复】与 STANDARD_TARGETS 里对应平台一起取消注释(目录仍由上面的 mkdir 预先创建)。
# cp "${OUTPUT_DIR}/bin/${APP_NAME}-linux-386" "${OUTPUT_DIR}/docker/linux/386/${APP_NAME}"
# cp "${OUTPUT_DIR}/bin/${APP_NAME}-linux-arm" "${OUTPUT_DIR}/docker/linux/arm/v7/${APP_NAME}"
# cp "${OUTPUT_DIR}/bin/${APP_NAME}-linux-arm64" "${OUTPUT_DIR}/docker/linux/arm64/${APP_NAME}"

# 每个平台只替换可执行文件名，许可证报告和发布文档保持一致。
GOFLAGS="-tags=jsoniter" go run github.com/google/go-licenses/v2@v2.0.1 report . \
    --ignore "github.com/bestruirui/${APP_NAME}" >"${OUTPUT_DIR}/THIRD_PARTY_LICENSES.csv"
cp README.md LICENSE "${OUTPUT_DIR}/THIRD_PARTY_LICENSES.csv" "${OUTPUT_DIR}/archives/"
for file in "${OUTPUT_DIR}"/bin/"${APP_NAME}"-*; do
    archive_name="$(basename "${file}").zip"
    executable_name="${APP_NAME}"
    if [[ "${file}" == *-windows-* ]]; then
        executable_name="${APP_NAME}.exe"
    fi
    cp "${file}" "${OUTPUT_DIR}/archives/${executable_name}"
    (cd "${OUTPUT_DIR}/archives" && zip -q "${archive_name}" "${executable_name}" README.md LICENSE THIRD_PARTY_LICENSES.csv)
    rm -f "${OUTPUT_DIR:?}/archives/${executable_name}"
done
rm -f "${OUTPUT_DIR:?}/archives/README.md" "${OUTPUT_DIR}/archives/LICENSE" \
    "${OUTPUT_DIR}/archives/THIRD_PARTY_LICENSES.csv"
(cd "${OUTPUT_DIR}/archives" && sha256sum ./*.zip >SHA256SUMS)
echo "Artifacts: ${OUTPUT_DIR}/archives"
