// Command benchmark records the optimization baseline without changing the
// gateway's protocol behavior. It runs a local in-process gateway against a
// deterministic httptest upstream and writes a Markdown report.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ai-gateway/internal/config"
	"ai-gateway/internal/logstore"
	"ai-gateway/internal/secret"
	"ai-gateway/internal/server"
)

type sample struct {
	latency time.Duration
	ttfb    time.Duration
}

type result struct {
	Concurrency int
	Requests    int
	Elapsed     time.Duration
	RequestsPS  float64
	P50         time.Duration
	P95         time.Duration
	TTFBP50     time.Duration
	TTFBP95     time.Duration
	HeapBase    uint64
	HeapPeak    uint64
	HeapDelta   uint64
}

func main() {
	requests := flag.Int("requests", 100, "requests per concurrency level")
	concurrencyRaw := flag.String("concurrency", "1,8,32", "comma-separated concurrency levels")
	output := flag.String("output", "", "Markdown output path; default docs/baseline-YYYY-MM-DD.md")
	releaseVersion := flag.String("release-version", "not-recorded", "phase-1 release version")
	releaseCommit := flag.String("release-commit", "not-recorded", "phase-1 release commit")
	releaseSHA := flag.String("release-sha256", "not-recorded", "phase-1 release archive SHA-256")
	platformNote := flag.String("platform-note", "not-recorded", "operating-system version note")
	flag.Parse()
	if *requests < 1 {
		fatalf("requests must be positive")
	}
	levels, err := parseLevels(*concurrencyRaw)
	if err != nil {
		fatalf("concurrency: %v", err)
	}
	if *output == "" {
		*output = filepath.Join("docs", "baseline-"+time.Now().Format("2006-01-02")+".md")
	}
	baselineCommit, baselineWorktree := gitState()

	root, err := os.MkdirTemp("", "ai-gateway-baseline-")
	if err != nil {
		fatalf("create temporary data root: %v", err)
	}
	defer os.RemoveAll(root)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var stream struct {
			Stream bool `json:"stream"`
		}
		_ = json.Unmarshal(body, &stream)
		if stream.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "data: {\"id\":\"baseline\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"baseline","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer upstream.Close()

	mgr := config.NewManager(filepath.Join(root, config.ConfigFileName))
	cfg := config.Defaults()
	cfg.Providers["openrouter"] = config.Provider{
		Name: "Baseline", Adapter: "openai-chat", BaseURL: upstream.URL,
		DefaultModel: "baseline-model",
	}
	cfg.Routes.Codex = config.Route{Provider: "openrouter", Model: "baseline-model"}
	cfg.Routes.Claude = cfg.Routes.Codex
	cfg.Routes.Grok = cfg.Routes.Codex
	cfg.Routes.Generic = cfg.Routes.Codex
	if err := mgr.Write(cfg); err != nil {
		fatalf("write benchmark config: %v", err)
	}
	gateway := server.New(mgr, secret.NewMemStore(), "baseline", os.Getpid())
	if err := gateway.Listen("127.0.0.1:0"); err != nil {
		fatalf("listen gateway: %v", err)
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- gateway.Serve() }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = gateway.Shutdown(ctx)
		<-serveErr
	}()

	client := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: maxInt(levels)}}
	endpoint := "http://" + gateway.Addr() + "/v1/chat/completions"
	requestBody := []byte(`{"model":"gateway-default","stream":true,"messages":[{"role":"user","content":"baseline"}]}`)
	for i := 0; i < 10; i++ {
		if _, err := doRequest(client, endpoint, requestBody); err != nil {
			fatalf("warm-up request: %v", err)
		}
	}
	if err := os.RemoveAll(filepath.Join(root, cfg.Logging.Dir)); err != nil {
		fatalf("clear temporary warm-up logs: %v", err)
	}
	results := make([]result, 0, len(levels))
	for _, level := range levels {
		measured, err := runWorkload(client, endpoint, requestBody, *requests, level)
		if err != nil {
			fatalf("run concurrency %d: %v", level, err)
		}
		results = append(results, measured)
	}

	logWriter := logstore.New(root)
	queryStart := time.Now()
	page, err := logWriter.List(cfg.Logging.Dir, logstore.Query{Limit: 500})
	if err != nil {
		fatalf("query logs: %v", err)
	}
	queryDuration := time.Since(queryStart)
	usageStart := time.Now()
	if _, err := logWriter.Usage(cfg.Logging.Dir, logstore.Query{}); err != nil {
		fatalf("query usage: %v", err)
	}
	usageDuration := time.Since(usageStart)
	logRoot := filepath.Join(root, cfg.Logging.Dir)
	diskBytes := directorySize(logRoot)
	logFiles := countFiles(logRoot)
	if err := writeReport(*output, upstream.URL, *requests, levels, results, logFiles, len(page.Items), queryDuration, usageDuration, diskBytes, *releaseVersion, *releaseCommit, *releaseSHA, *platformNote, baselineCommit, baselineWorktree); err != nil {
		fatalf("write report: %v", err)
	}
	fmt.Printf("baseline written to %s\n", *output)
}

func runWorkload(client *http.Client, endpoint string, body []byte, requests, concurrency int) (result, error) {
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	var peak atomic.Uint64
	peak.Store(before.HeapAlloc)
	stopSampling := make(chan struct{})
	samplingDone := make(chan struct{})
	go func() {
		defer close(samplingDone)
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				var mem runtime.MemStats
				runtime.ReadMemStats(&mem)
				updatePeak(&peak, mem.HeapAlloc)
			case <-stopSampling:
				return
			}
		}
	}()
	started := time.Now()
	var next atomic.Int64
	var wg sync.WaitGroup
	samples := make([]sample, requests)
	errors := make(chan error, concurrency)
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				idx := int(next.Add(1)) - 1
				if idx >= requests {
					return
				}
				measured, err := doRequest(client, endpoint, body)
				if err != nil {
					errors <- err
					return
				}
				samples[idx] = measured
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(started)
	close(stopSampling)
	<-samplingDone
	close(errors)
	if err := <-errors; err != nil {
		return result{}, err
	}
	latencies := make([]time.Duration, 0, len(samples))
	ttfb := make([]time.Duration, 0, len(samples))
	for _, s := range samples {
		latencies = append(latencies, s.latency)
		ttfb = append(ttfb, s.ttfb)
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	sort.Slice(ttfb, func(i, j int) bool { return ttfb[i] < ttfb[j] })
	peakBytes := peak.Load()
	return result{
		Concurrency: concurrency,
		Requests:    requests,
		Elapsed:     elapsed,
		RequestsPS:  float64(requests) / elapsed.Seconds(),
		P50:         percentile(latencies, .50),
		P95:         percentile(latencies, .95),
		TTFBP50:     percentile(ttfb, .50),
		TTFBP95:     percentile(ttfb, .95),
		HeapBase:    before.HeapAlloc,
		HeapPeak:    peakBytes,
		HeapDelta:   peakBytes - before.HeapAlloc,
	}, nil
}

func doRequest(client *http.Client, endpoint string, body []byte) (sample, error) {
	started := time.Now()
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return sample{}, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return sample{}, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return sample{}, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	buf := make([]byte, 1)
	_, err = resp.Body.Read(buf)
	if err != nil && err != io.EOF {
		return sample{}, fmt.Errorf("read first response byte: %w", err)
	}
	ttfb := time.Since(started)
	_, _ = io.Copy(io.Discard, resp.Body)
	return sample{latency: time.Since(started), ttfb: ttfb}, nil
}

func writeReport(path, upstream string, requests int, levels []int, results []result, logFiles, logItems int, logQuery, usageQuery time.Duration, diskBytes int64, releaseVersion, releaseCommit, releaseSHA, platformNote, baselineCommit, baselineWorktree string) error {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	date := time.Now().Format("2006-01-02")
	fmt.Fprintf(f, "# ai-gateway 优化前基线\n\n")
	fmt.Fprintf(f, "> 日期：%s\n> 工具：`cmd/benchmark`\n> 第一阶段发布：`%s`，提交 `%s`，SHA-256 `%s`\n> 基准代码提交：`%s`\n> 工作区：`%s`\n> 平台：`%s/%s`，`%s`，逻辑处理器 `%d`\n> 系统说明：%s\n> 数据根：临时目录（运行结束后删除）\n\n", date, releaseVersion, releaseCommit, releaseSHA, baselineCommit, baselineWorktree, runtime.GOOS, runtime.GOARCH, runtime.Version(), runtime.NumCPU(), platformNote)
	fmt.Fprintf(f, "## 范围\n\n本报告建立 `G0-02` 的测量基线，不改变协议、配置、路由、日志合同或发布行为。正式冻结前应在干净提交上重跑。请求通过本地确定性上游，夹具为固定的流式 Chat 请求；内存指标是基准进程的 Go `HeapAlloc` 峰值，不等同于操作系统工作集。\n\n")
	fmt.Fprintf(f, "- 上游地址：`%s`（仅本次运行）\n- 预热请求数：`10`（预热日志在正式测量前删除）\n- 每档请求数：`%d`\n- 并发档位：`%s`\n- 日志文件数：`%d`\n- 日志列表返回数：`%d`\n- 日志目录磁盘占用：`%d` 字节\n- 日志列表查询：`%s`\n- 用量查询：`%s`\n\n", upstream, requests, joinInts(levels), logFiles, logItems, diskBytes, logQuery, usageQuery)
	fmt.Fprintf(f, "## 请求基线\n\n| 并发 | 请求数 | 总耗时 | 每秒请求 | 延迟 P50 | 延迟 P95 | 首字节 P50 | 首字节 P95 | HeapAlloc 基线 | HeapAlloc 峰值 | HeapAlloc 增量 |\n|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, r := range results {
		fmt.Fprintf(f, "| %d | %d | %s | %.2f | %s | %s | %s | %s | %d | %d | %d |\n", r.Concurrency, r.Requests, r.Elapsed, r.RequestsPS, r.P50, r.P95, r.TTFBP50, r.TTFBP95, r.HeapBase, r.HeapPeak, r.HeapDelta)
	}
	if baselineWorktree == "clean" {
		fmt.Fprintf(f, "\n## 结论\n\n该报告提供后续 `SEC-01`、`RES-01`、`PERF-01`、`LOG-01` 和 `OBS-01` 实施前的可重复比较点。第一期已按产品决策以遗留问题方式验收关闭；本次运行从干净提交开始，`G0-02` 基线已冻结，可以进入发布级优化实现。\n")
	} else {
		fmt.Fprintf(f, "\n## 结论\n\n该报告提供后续 `SEC-01`、`RES-01`、`PERF-01`、`LOG-01` 和 `OBS-01` 实施前的可重复比较点。第一期已按产品决策以遗留问题方式验收关闭；本次运行不是干净工作区，`G0-02` 仍需在干净提交上重跑并冻结。\n")
	}
	return nil
}

func parseLevels(raw string) ([]int, error) {
	parts := strings.Split(raw, ",")
	levels := make([]int, 0, len(parts))
	seen := map[int]bool{}
	for _, part := range parts {
		v, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || v < 1 || seen[v] {
			return nil, fmt.Errorf("invalid or duplicate level %q", part)
		}
		seen[v] = true
		levels = append(levels, v)
	}
	sort.Ints(levels)
	return levels, nil
}

func percentile(values []time.Duration, p float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	idx := int(math.Ceil(float64(len(values))*p)) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(values) {
		idx = len(values) - 1
	}
	return values[idx]
}

func directorySize(root string) int64 {
	var total int64
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}

func countFiles(root string) int {
	count := 0
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() {
			count++
		}
		return nil
	})
	return count
}

func joinInts(values []int) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, ", ")
}

func maxInt(values []int) int {
	max := 1
	for _, value := range values {
		if value > max {
			max = value
		}
	}
	return max
}

func updatePeak(peak *atomic.Uint64, value uint64) {
	for {
		old := peak.Load()
		if value <= old || peak.CompareAndSwap(old, value) {
			return
		}
	}
}

func gitState() (string, string) {
	commit := "unknown"
	if data, err := exec.Command("git", "rev-parse", "HEAD").Output(); err == nil {
		commit = strings.TrimSpace(string(data))
	}
	dirty := "unknown"
	if data, err := exec.Command("git", "status", "--porcelain").Output(); err == nil {
		if len(bytes.TrimSpace(data)) == 0 {
			dirty = "clean"
		} else {
			dirty = "dirty"
		}
	}
	return commit, dirty
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
