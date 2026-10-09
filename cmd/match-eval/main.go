package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/analysis"
	"github.com/KanaDoodle/CampusTrace/internal/matcheval"
	"github.com/KanaDoodle/CampusTrace/internal/modelconfig"
)

type paths []string

func (p *paths) String() string     { return strings.Join(*p, ", ") }
func (p *paths) Set(v string) error { *p = append(*p, v); return nil }

func readJSON(path string, out any) error {
	f, e := os.Open(path)
	if e != nil {
		return errors.New("无法打开输入文件")
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, (10<<20)+1))
	if e != nil || len(b) > 10<<20 {
		return errors.New("输入文件超过10MiB或无法读取")
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		return errors.New("JSON格式或字段不符合评测格式")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return errors.New("JSON文件包含额外内容")
	}
	return nil
}
func main() {
	var input paths
	flag.Var(&input, "dataset", "评测案例JSON文件，可重复指定以合并案例")
	responses := flag.String("responses", "", "替换已记录结果的JSON文件：{trials:[...]}，用于比较其他模型/版本")
	live := flag.Bool("live", false, "明确允许将案例脱敏材料发送给指定外部模型并产生API费用")
	url := flag.String("url", "", "公开HTTPS Chat Completions地址")
	model := flag.String("model", "", "模型名称")
	keyEnv := flag.String("key-env", "", "保存API密钥的环境变量名称")
	only := flag.String("only", "", "仅运行指定案例ID")
	limit := flag.Int("limit", 0, "最大案例数量，0表示全部")
	attempts := flag.Int("attempts", 1, "每个案例运行次数，1..3，仅live模式")
	maxCalls := flag.Int("max-calls", 30, "外部请求上限，1..192，超限时在任何调用前拒绝")
	strict := flag.Bool("strict", false, "核对失败或人工标注不一致时返回非零退出码")
	flag.Parse()
	if len(input) == 0 {
		fail("请用 -dataset 指定案例；默认不读取数据库、不调用外部模型")
	}
	if *limit < 0 || *attempts < 1 || *attempts > 3 || *maxCalls < 1 || *maxCalls > 192 {
		fail("案例数量、重复次数或请求上限无效")
	}
	if *live && *responses != "" {
		fail("外部调用与离线结果回放不能同时使用")
	}
	if !*live && (*url != "" || *model != "" || *keyEnv != "") {
		fail("外部调用必须显式添加 -live；当前未发起请求")
	}
	dataset := matcheval.Dataset{Version: matcheval.Version}
	for _, path := range input {
		var d matcheval.Dataset
		if e := readJSON(path, &d); e != nil {
			fail(e.Error())
		}
		if e := d.Validate(); e != nil {
			fail(e.Error())
		}
		dataset.Cases = append(dataset.Cases, d.Cases...)
		dataset.Recorded = append(dataset.Recorded, d.Recorded...)
	}
	if *responses != "" {
		var doc struct {
			Trials []matcheval.Trial `json:"trials"`
		}
		if e := readJSON(*responses, &doc); e != nil {
			fail(e.Error())
		}
		dataset.Recorded = doc.Trials
	}
	if e := dataset.Validate(); e != nil {
		fail(e.Error())
	}
	cases := []matcheval.Case{}
	for _, c := range dataset.Cases {
		if *only == "" || c.ID == *only {
			cases = append(cases, c)
		}
	}
	if *limit > 0 && len(cases) > *limit {
		cases = cases[:*limit]
	}
	if len(cases) == 0 {
		fail("没有符合条件的案例")
	}
	byID := map[string]matcheval.Case{}
	for _, c := range cases {
		byID[c.ID] = c
	}
	trials := []matcheval.Trial{}
	mode := "recorded-results-replay"
	if *live {
		if len(cases)**attempts > *maxCalls {
			fail("所选案例超过请求上限；请减少案例或次数，尚未发起模型请求")
		}
		cfg := modelconfig.Config{URL: *url, Model: *model, APIKey: os.Getenv(*keyEnv)}
		if e := cfg.Validate(); e != nil {
			fail("模型配置无效或密钥环境变量为空")
		}
		client := analysis.NewChat(cfg.URL, cfg.APIKey, cfg.Model, 1)
		client.HTTP = modelconfig.PublicClient()
		client.OutputTokenLimit = 8192
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		mode = "explicit-live-model-evaluation"
		for _, c := range cases {
			for attempt := 1; attempt <= *attempts; attempt++ {
				if ctx.Err() != nil {
					fail("评测已取消")
				}
				bounded, cancel := context.WithTimeout(ctx, 90*time.Second)
				trials = append(trials, matcheval.RunLive(bounded, c, attempt, client))
				cancel()
			}
		}
	} else {
		seen := map[string]bool{}
		for _, t := range dataset.Recorded {
			if _, ok := byID[t.CaseID]; ok {
				trials = append(trials, t)
				seen[t.CaseID] = true
			}
		}
		for _, c := range cases {
			if !seen[c.ID] {
				trials = append(trials, matcheval.Trial{CaseID: c.ID, Attempt: 1, ErrorCode: "NO_RECORDED_RESULT"})
			}
		}
	}
	scores := []matcheval.Score{}
	for _, t := range trials {
		scores = append(scores, matcheval.Grade(byID[t.CaseID], t))
	}
	report := struct {
		matcheval.Summary
		Results []matcheval.Trial `json:"trials"`
	}{matcheval.Summarize(mode, scores), trials}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if enc.Encode(report) != nil {
		fail("无法写出评测报告")
	}
	if *strict {
		for _, s := range scores {
			if !s.ContractPass || s.TopAgreement != nil && !*s.TopAgreement || s.PairCorrect < s.PairTotal {
				os.Exit(1)
			}
		}
	}
}
func fail(message string) { fmt.Fprintln(os.Stderr, "CampusTrace 匹配评测："+message); os.Exit(2) }
