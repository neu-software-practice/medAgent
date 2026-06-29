package ai

import (
	"context"
	"encoding/json"
)

// FakeLLM 是测试用的可编程 LLMClient。On 既能断言入参又能返回构造输出。
type FakeLLM struct {
	On func(req CompletionRequest) (CompletionResult, error)
}

func (f *FakeLLM) Complete(ctx context.Context, req CompletionRequest) (CompletionResult, error) {
	if err := ctx.Err(); err != nil {
		return CompletionResult{}, err
	}
	return f.On(req)
}

// StructuredOf 把任意值 marshal 成 CompletionResult.Structured，便于构造预设输出。
func StructuredOf(v any) CompletionResult {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // 测试辅助：输入应可序列化
	}
	return CompletionResult{Structured: b, Raw: string(b)}
}

// FakeChat 是测试用的可编程 ChatClient。OnChat 既能断言入参又能返回构造产出。
type FakeChat struct {
	OnChat func(req ChatRequest) (AssistantTurn, error)
}

func (f *FakeChat) Chat(ctx context.Context, req ChatRequest) (AssistantTurn, error) {
	if err := ctx.Err(); err != nil {
		return AssistantTurn{}, err
	}
	return f.OnChat(req)
}

// ToolCallTurn 构造一个带单个工具调用的 AssistantTurn，便于脚本化测试。
func ToolCallTurn(id, name string, args any) AssistantTurn {
	b, err := json.Marshal(args)
	if err != nil {
		panic(err) // 测试辅助：输入应可序列化
	}
	return AssistantTurn{ToolCalls: []ToolCall{{ID: id, Name: name, Arguments: b}}}
}
