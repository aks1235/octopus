package update

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"

	"github.com/bestruirui/octopus/internal/utils/shutdown"
	"github.com/charmbracelet/log"
)

// UpdateCore 下载并替换当前可执行文件, 返回被替换的文件路径。
//
// 返回路径而不是让调用方自己再取一次: 替换过程会把原文件 rename 成 .old, 此时
// os.Executable() 在 Linux 上(读 /proc/self/exe)会跟着 inode 指到那个 .old 路径,
// 而 .old 随后就被删掉了。重启时若重新取路径, 拿到的就是一个不存在的文件 ——
// 这正是 v2.1.16 上"更新成功、重启失败"的原因。
// errUpdateInProgress 表示已有更新在跑。
var errUpdateInProgress = errors.New("update already in progress")

var (
	// updateMu 保证同一时刻只有一个更新流程。两个并发请求会交叉 rename/copy 同一份
	// 可执行文件: 一方刚把文件 rename 成 .old, 另一方就找不到源文件; 更糟的是两次
	// copy 交替写同一路径, 可能留下半截二进制, 只能外部重装容器才能恢复。
	updateMu sync.Mutex
	// updateCoreImpl 指向实际实现, 抽成变量只为测试注入。
	updateCoreImpl = updateCoreLocked
)

func UpdateCore() (string, error) {
	if !updateMu.TryLock() {
		return "", errUpdateInProgress
	}
	defer updateMu.Unlock()
	return updateCoreImpl()
}

func updateCoreLocked() (string, error) {
	log.Infof("start update core")

	filename, err := getDownloadFilename()
	if err != nil {
		log.Warnf("update core failed: %v", err)
		return "", err
	}

	downloadUrl := updateUrl + "/" + filename
	log.Infof("download url: %s", downloadUrl)
	data, err := doRequestWithFallback(downloadUrl)
	if err != nil {
		log.Warnf("download failed: %v", err)
		return "", err
	}

	execPath, err := os.Executable()
	if err != nil {
		log.Warnf("get executable path failed: %v", err)
		return "", err
	}
	execName := filepath.Base(execPath)

	tmpDir, err := os.MkdirTemp("", execName+"-update-*")
	if err != nil {
		log.Warnf("create temp dir failed: %v", err)
		return "", err
	}
	defer os.RemoveAll(tmpDir)
	log.Infof("using temp dir: %s", tmpDir)

	if err := unzip(data, tmpDir); err != nil {
		log.Warnf("unzip failed: %v", err)
		return "", err
	}

	newExec := filepath.Join(tmpDir, execName)
	if info, err := os.Stat(newExec); err != nil || info.IsDir() {
		log.Warnf("new executable not found at %s: %v", newExec, err)
		return "", fmt.Errorf("new executable not found in archive root: %w", err)
	}
	log.Infof("new executable: %s", newExec)

	oldPath := execPath + ".old"
	if err := os.Rename(execPath, oldPath); err != nil {
		log.Warnf("rename old executable failed: %v", err)
		return "", err
	}

	if err := copyFile(newExec, execPath); err != nil {
		log.Errorf("replace executable failed, try to restore: %v", err)
		_ = os.Rename(oldPath, execPath)
		return "", err
	}

	if info, statErr := os.Stat(oldPath); statErr == nil {
		_ = os.Chmod(execPath, info.Mode().Perm())
	}
	_ = os.RemoveAll(oldPath)

	log.Infof("update core success, restart from %s", execPath)
	return execPath, nil
}

// Restart 关闭服务并把进程交给 execPath 上的二进制。
//
// execPath 必须由 UpdateCore 传回, 不能在这里重新调 os.Executable(): 替换过程已把原文件
// rename 成 .old 并删除, /proc/self/exe 此时指向的是那个已消失的旧路径。
//
// 只能由调用方在 HTTP 响应已经写出之后调用: 关闭流程会关停 HTTP 服务器并掐断在途连接,
// 若在响应写完之前重启, 客户端收到的是网络错误而不是 "update success"。
func Restart(execPath string) {
	if execPath == "" {
		log.Errorf("restart skipped: empty executable path")
		return
	}
	// 先确认目标可执行再关服务。shutdown 之后才发现路径不可用, 进程会停在
	// "HTTP 已关停、数据库已释放" 的半死状态: 端口不通, 日志里持续刷 database is closed,
	// 只能靠外部重启容器才能恢复。宁可这次不升级, 也不要让服务停在这种状态。
	if _, err := os.Stat(execPath); err != nil {
		log.Errorf("restart skipped, %q not usable: %v", execPath, err)
		return
	}
	restartExecutable(execPath)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), os.ModePerm); err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

// getDownloadFilename 返回当前平台对应的发布归档名称。
func getDownloadFilename() (string, error) {
	arch := runtime.GOARCH
	goos := runtime.GOOS

	switch goos {
	case "windows":
		switch arch {
		case "amd64":
			return "octopus-windows-amd64.zip", nil
		}
	case "darwin":
		switch arch {
		case "amd64":
			return "octopus-darwin-amd64.zip", nil
		case "arm64":
			return "octopus-darwin-arm64.zip", nil
		}
	case "linux":
		switch arch {
		case "386":
			return "octopus-linux-386.zip", nil
		case "amd64":
			return "octopus-linux-amd64.zip", nil
		case "arm":
			return "octopus-linux-arm.zip", nil
		case "arm64":
			return "octopus-linux-arm64.zip", nil
		}
	case "android":
		switch arch {
		case "386":
			return "octopus-android-386.zip", nil
		case "amd64":
			return "octopus-android-amd64.zip", nil
		case "arm":
			return "octopus-android-arm.zip", nil
		case "arm64":
			return "octopus-android-arm64.zip", nil
		}
	}
	return "", fmt.Errorf("unsupported platform: %s/%s", goos, arch)
}

func restartExecutable(execPath string) {
	shutdown.Shutdown()

	log.Infof("restarting: %q %q", execPath, os.Args[1:])

	if runtime.GOOS == "windows" {
		cmd := exec.Command(execPath, os.Args[1:]...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			log.Errorf("restarting failed: %v", err)
			// 服务已关停、数据库已释放, 留在原地只会是个拿到请求就报错的空壳;
			// 以非零码退出, 交给容器的重启策略拉起(退出码 0 会被 restart: on-failure 忽略)。
			os.Exit(1)
		}
		os.Exit(0)
	}

	if err := syscall.Exec(execPath, os.Args, os.Environ()); err != nil {
		log.Errorf("restarting failed: %v", err)
		// 同 Windows 分支: 此时 HTTP 服务器与数据库都已关停, 继续运行没有意义;
		// 退出让容器重启, 至少能恢复到可用状态。
		os.Exit(1)
	}
}
