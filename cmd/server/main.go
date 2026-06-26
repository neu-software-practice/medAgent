// Command server 起 medagent HTTP 服务。
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"medagent/agent"
)

func main() {
	addr := flag.String("addr", ":8080", "监听地址")
	provider := flag.String("provider", "deepseek", "provider: deepseek|qwen|openai")
	model := flag.String("model", "deepseek-chat", "模型名")
	baseURL := flag.String("base-url", "", "覆盖 base URL")
	logDir := flag.String("log-dir", "./logs", "诊疗日志目录")
	caps := flag.String("caps", "", "本院能力清单，逗号分隔")
	flag.Parse()

	keyEnv := map[string]string{"deepseek": "DEEPSEEK_API_KEY", "qwen": "DASHSCOPE_API_KEY", "openai": "OPENAI_API_KEY"}[*provider]
	if keyEnv == "" {
		log.Fatalf("未知 provider %q，支持 deepseek|qwen|openai", *provider)
	}
	key := os.Getenv(keyEnv)
	if key == "" {
		log.Fatalf("缺少环境变量 %s", keyEnv)
	}
	capSet := map[string]bool{}
	for _, c := range strings.Split(*caps, ",") {
		if c = strings.TrimSpace(c); c != "" {
			capSet[c] = true
		}
	}
	svc, err := agent.New(agent.Config{
		Provider: *provider, APIKey: key, Model: *model, BaseURL: *baseURL,
		LogDir: *logDir, Caps: capSet,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer svc.Close()
	log.Printf("medagent 服务监听 %s（provider=%s model=%s）", *addr, *provider, *model)
	srv := &http.Server{
		Addr:              *addr,
		Handler:           svc.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      5 * time.Minute, // 一轮可能含多次 LLM 调用
		IdleTimeout:       120 * time.Second,
	}
	log.Fatal(srv.ListenAndServe())
}
