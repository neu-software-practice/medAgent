package ai

import (
	"strings"
	"testing"
)

func TestBuildMessagesIncludesStructuredFields(t *testing.T) {
	s := Snapshot{
		Subjective:  map[string]any{"体温": "38.5", "主诉": "咽痛"},
		TestResults: []TestResult{{Item: "血常规", Value: "淋巴偏高"}},
		Diagnosis:   &Diagnosis{Name: "急性咽炎", Basis: "症状+检验", Confidence: 0.9},
	}
	msgs := buildMessages(s)
	if len(msgs) == 0 || msgs[0].Role != "user" {
		t.Fatalf("首条应为 user 快照块，得到 %+v", msgs)
	}
	block := msgs[0].Content
	for _, want := range []string{"体温", "主诉", "血常规", "急性咽炎"} {
		if !strings.Contains(block, want) {
			t.Fatalf("快照块缺少 %q：\n%s", want, block)
		}
	}
	// 主观信息按 key 排序，输出确定性："主诉" 应在 "体温" 之前（按 Unicode 码点）
	if strings.Index(block, "主诉") > strings.Index(block, "体温") {
		t.Fatal("主观信息未按 key 稳定排序")
	}
}

func TestBuildMessagesNoRoundCounters(t *testing.T) {
	// Snapshot 根本不含轮次计数；断言渲染结果不泄漏任何轮次字样。
	s := Snapshot{Subjective: map[string]any{"主诉": "咽痛"}}
	block := buildMessages(s)[0].Content
	for _, leak := range []string{"轮次", "InterviewRounds", "TestRounds", "rounds"} {
		if strings.Contains(block, leak) {
			t.Fatalf("快照块泄漏轮次信息 %q", leak)
		}
	}
}

func TestBuildMessagesRecentTurnsBecomeMessages(t *testing.T) {
	s := Snapshot{Interview: []DialogTurn{
		{Role: "patient", Text: "嗓子痛"},
		{Role: "doctor", Text: "发烧吗"},
		{Role: "patient", Text: "38.5"},
	}}
	msgs := buildMessages(s)
	// 1 条快照块 + 3 条原文
	if len(msgs) != 4 {
		t.Fatalf("期望 4 条消息，得到 %d", len(msgs))
	}
	if msgs[1].Role != "user" || msgs[1].Content != "嗓子痛" {
		t.Fatalf("patient→user 映射错误：%+v", msgs[1])
	}
	if msgs[2].Role != "assistant" || msgs[2].Content != "发烧吗" {
		t.Fatalf("doctor→assistant 映射错误：%+v", msgs[2])
	}
}

func TestBuildMessagesCompressesEarlyTurns(t *testing.T) {
	var turns []DialogTurn
	for i := 0; i < InterviewRawTurns+4; i++ {
		turns = append(turns, DialogTurn{Role: "patient", Text: "早期对话"})
	}
	turns = append(turns, DialogTurn{Role: "patient", Text: "最新一句"})
	s := Snapshot{Interview: turns}
	msgs := buildMessages(s)
	// 最近 InterviewRawTurns 轮成原文消息，其余进 digest（在快照块里）
	if got := len(msgs) - 1; got != InterviewRawTurns {
		t.Fatalf("期望 %d 条原文消息，得到 %d", InterviewRawTurns, got)
	}
	if !strings.Contains(msgs[0].Content, "早期对话摘要") {
		t.Fatalf("快照块缺少早期对话摘要：\n%s", msgs[0].Content)
	}
}

func TestBuildMessagesRendersFeedback(t *testing.T) {
	s := Snapshot{Feedback: &OrchestratorFeedback{
		LastReject:  RejectSubjectiveNotExhausted,
		MissingHint: []string{"基础病史", "用药史"},
	}}
	block := buildMessages(s)[0].Content
	if !strings.Contains(block, string(RejectSubjectiveNotExhausted)) {
		t.Fatal("未渲染 LastReject")
	}
	if !strings.Contains(block, "基础病史") {
		t.Fatal("未渲染 MissingHint")
	}
}
