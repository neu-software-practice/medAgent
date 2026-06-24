package ai

import "context"

// interviewAgent 是问诊 agent：采集主观信息、对话追问。
type interviewAgent struct{ llm LLMClient }

func (a interviewAgent) system() string { return promptInterview }

func (a interviewAgent) decide(ctx context.Context, s Snapshot) (InterviewResult, error) {
	return runStructured[InterviewResult](ctx, a.llm, "interview", promptInterview, schemaInterview, s)
}
