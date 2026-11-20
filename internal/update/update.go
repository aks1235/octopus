package update

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/rhttp"
	"github.com/charmbracelet/log"
)

// 更新地址由 conf.Repo 拼接:ldflags 注入或默认值兜底,保证在线更新始终指向自有仓库。
var (
	repoSlug     = strings.TrimPrefix(strings.TrimSuffix(conf.Repo, "/"), "https://github.com/")
	updateUrl    = "https://github.com/" + repoSlug + "/releases/latest/download"
	updateApiUrl = "https://api.github.com/repos/" + repoSlug + "/releases/latest"
)

type LatestInfo struct {
	TagName     string `json:"tag_name"`
	PublishedAt string `json:"published_at"`
	Body        string `json:"body"`
	Message     string `json:"message"`
}

var github_pat = os.Getenv(strings.ToUpper(conf.APP_NAME) + "_GITHUB_PAT")

// doRequestWithFallback performs an HTTP GET request, first without proxy, then with proxy if failed.
func doRequestWithFallback(url string) ([]byte, error) {
	data, err := doRequest(url, false)
	if err == nil {
		return data, nil
	}
	log.Warnf("direct request failed, trying with proxy: %v", err)
	return doRequest(url, true)
}

func doRequest(url string, useProxy bool) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var hc *http.Client
	var err error
	if useProxy {
		hc, err = rhttp.Proxy()
	} else {
		hc, err = rhttp.Direct()
	}
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		log.Debugf("new request failed: %v", err)
		return nil, err
	}

	if github_pat != "" {
		req.Header.Set("Authorization", "Bearer "+github_pat)
	}

	resp, err := hc.Do(req)
	if err != nil {
		log.Debugf("request failed: %v", err)
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Debugf("read body failed: %v", err)
		return nil, err
	}
	return data, nil
}

// latestCacheTTL 是发布信息的内存缓存时长。
//
// GitHub 未认证接口按来源 IP 限流(60 次/小时), 而设置页每次挂载都会查一次版本;
// 同一出口 IP 下多标签页、多人或多实例共用额度时很容易打满, 打满后接口报错,
// 页面就显示「最新版本 未知」——看起来像发布没成功, 实际只是被限流。
// 缓存后单个实例每小时只需几次请求。
const latestCacheTTL = 10 * time.Minute

var (
	latestMu        sync.Mutex
	latestCached    *LatestInfo
	latestFetchedAt time.Time
)

// fetchLatestFn 是取发布信息的实际实现, 抽成变量只为测试注入; 生产路径恒为 fetchLatestInfo。
var fetchLatestFn = fetchLatestInfo

// GetLatestInfo 返回最新发布信息, 命中缓存时不再请求 GitHub。
func GetLatestInfo() (*LatestInfo, error) {
	latestMu.Lock()
	defer latestMu.Unlock()

	if latestCached != nil && time.Since(latestFetchedAt) < latestCacheTTL {
		cached := *latestCached
		return &cached, nil
	}

	latestInfo, err := fetchLatestFn()
	if err != nil {
		return staleLatestInfo(err)
	}

	latestCached = latestInfo
	latestFetchedAt = time.Now()
	cached := *latestInfo
	return &cached, nil
}

// fetchLatestInfo 真正请求 GitHub 发布接口。
func fetchLatestInfo() (*LatestInfo, error) {
	body, err := doRequestWithFallback(updateApiUrl)
	if err != nil {
		return nil, err
	}

	var latestInfo LatestInfo
	if err := json.Unmarshal(body, &latestInfo); err != nil {
		log.Debugf("unmarshal body failed: %v", err)
		return nil, err
	}
	if latestInfo.Message != "" {
		return nil, fmt.Errorf("failed to get latest info: %s", latestInfo.Message)
	}
	return &latestInfo, nil
}

// staleLatestInfo 在拉取失败时退回上一次成功的结果。
//
// 版本号本身变化很慢, 展示几分钟前的旧值远好于把「未知」抛给用户;
// 限流这类瞬时故障恢复后, 下一轮请求会自然刷成最新值。
func staleLatestInfo(cause error) (*LatestInfo, error) {
	if latestCached == nil {
		return nil, cause
	}
	log.Warnf("get latest info failed, serving cached result from %s: %v", latestFetchedAt.Format(time.RFC3339), cause)
	cached := *latestCached
	return &cached, nil
}

func unzip(data []byte, dest string) error {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		log.Debugf("new zip reader failed: %v", err)
		return err
	}

	for _, f := range r.File {
		fpath := filepath.Join(dest, f.Name)

		if !isPathInDest(fpath, dest) {
			log.Debugf("invalid file path: %s", fpath)
			return fmt.Errorf("invalid file path: %s", fpath)
		}

		info := f.FileInfo()
		if info.IsDir() {
			os.MkdirAll(fpath, os.ModePerm)
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}

		if err := extractFile(f, fpath); err != nil {
			return err
		}
	}
	return nil
}

func extractFile(f *zip.File, fpath string) error {
	if err := os.MkdirAll(filepath.Dir(fpath), os.ModePerm); err != nil {
		log.Debugf("mkdir all failed: %v", err)
		return err
	}

	outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode().Perm())
	if err != nil {
		if err = os.Remove(fpath); err != nil {
			log.Debugf("remove file failed: %v", err)
			return err
		}
		outFile, err = os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			log.Debugf("open file failed: %v", err)
			return err
		}
	}
	defer outFile.Close()

	rc, err := f.Open()
	if err != nil {
		log.Debugf("open file failed: %v", err)
		return err
	}
	defer rc.Close()

	if _, err = io.Copy(outFile, rc); err != nil {
		log.Debugf("copy failed: %v", err)
		return err
	}
	return nil
}

func isPathInDest(fpath, dest string) bool {
	rel, err := filepath.Rel(dest, fpath)
	if err != nil {
		return false
	}
	return filepath.IsLocal(rel)
}
