package ai

import (
	"fmt"
	"sort"
	"strings"
)

// buildMessages 从 Snapshot 重建 LLM 上下文。
// 结构化字段全量带入；Interview 仅保留最近 InterviewRawTurns 轮原文，
// 更早的折叠成 digest 行并入快照块（阳性发现/关键体征不丢——按原文保留）。
func buildMessages(s Snapshot) []Message {
	split := len(s.Interview) - InterviewRawTurns
	if split < 0 {
		split = 0
	}
	early := s.Interview[:split]
	recent := s.Interview[split:]

	msgs := []Message{{Role: "user", Content: renderSnapshotBlock(s, early)}}
	for _, tn := range recent {
		msgs = append(msgs, Message{Role: roleOf(tn.Role), Content: tn.Text})
	}
	return msgs
}

func roleOf(turn string) string {
	if turn == "doctor" {
		return "assistant"
	}
	return "user"
}

func renderSnapshotBlock(s Snapshot, early []DialogTurn) string {
	var b strings.Builder
	b.WriteString("【就诊快照】\n")

	if len(s.Profile) > 0 {
		fmt.Fprintf(&b, "【患者资料】%s\n", s.Profile)
	}
	if s.History != "" {
		fmt.Fprintf(&b, "【历史就诊记录】\n%s\n", s.History)
	}

	if len(s.Subjective) > 0 {
		b.WriteString("主观信息:\n")
		keys := make([]string, 0, len(s.Subjective))
		for k := range s.Subjective {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "  - %s: %v\n", k, s.Subjective[k])
		}
	}
	if len(s.TestResults) > 0 {
		b.WriteString("检验结果:\n")
		for _, r := range s.TestResults {
			fmt.Fprintf(&b, "  - %s: %s\n", r.Item, r.Value)
		}
	}
	if s.Diagnosis != nil {
		fmt.Fprintf(&b, "已确诊: %s（依据: %s，置信度 %.2f）\n",
			s.Diagnosis.Name, s.Diagnosis.Basis, s.Diagnosis.Confidence)
	}
	if s.PriorVisit != nil && s.PriorVisit.Diagnosis != nil {
		fmt.Fprintf(&b, "上次就诊: %s\n", s.PriorVisit.Diagnosis.Name)
	}
	if len(s.Refusals) > 0 {
		b.WriteString("患者拒绝记录:\n")
		for _, r := range s.Refusals {
			fmt.Fprintf(&b, "  - %s\n", r.What)
		}
	}
	if len(early) > 0 {
		b.WriteString("早期对话记录:\n")
		for _, tn := range early {
			who := "患者"
			if tn.Role == "doctor" {
				who = "医生"
			}
			fmt.Fprintf(&b, "  · %s: %s\n", who, tn.Text)
		}
	}
	if s.Feedback != nil {
		renderFeedback(&b, s.Feedback)
	}
	return b.String()
}

func renderFeedback(b *strings.Builder, f *OrchestratorFeedback) {
	b.WriteString("【编排反馈】\n")
	if f.LastReject != RejectNone {
		fmt.Fprintf(b, "上次意图被拒: %s\n", f.LastReject)
	}
	if len(f.MissingHint) > 0 {
		fmt.Fprintf(b, "需补充: %s\n", strings.Join(f.MissingHint, "、"))
	}
	if f.NextExpected != "" {
		fmt.Fprintf(b, "期望产出: %s\n", f.NextExpected)
	}
	if f.CardDeferred {
		b.WriteString("上一张卡已被暂缓\n")
	}
}
