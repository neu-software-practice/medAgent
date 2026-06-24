package ai

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestGuardianHit(t *testing.T) {
	llm := &FakeLLM{On: func(req CompletionRequest) (CompletionResult, error) {
		if !strings.Contains(req.Messages[len(req.Messages)-1].Content, "胸痛") {
			t.Fatal("事件未注入上下文")
		}
		return StructuredOf(emergencyWire{Hit: true, Reason: "胸痛伴呼吸困难"}), nil
	}}
	ei, hit, err := guardianAgent{llm: llm}.Assess(context.Background(), Snapshot{}, Event{Kind: "vital", Data: "胸痛"})
	if err != nil || !hit || ei.Reason == "" {
		t.Fatalf("期望命中，得到 ei=%+v hit=%v err=%v", ei, hit, err)
	}
}

func TestGuardianNoHit(t *testing.T) {
	llm := &FakeLLM{On: func(CompletionRequest) (CompletionResult, error) {
		return StructuredOf(emergencyWire{Hit: false}), nil
	}}
	_, hit, err := guardianAgent{llm: llm}.Assess(context.Background(), Snapshot{}, Event{Kind: "dialog", Data: "嗓子痛"})
	if err != nil || hit {
		t.Fatalf("期望不命中，得到 hit=%v err=%v", hit, err)
	}
}

func TestGuardianLLMErrorDoesNotFabricateHit(t *testing.T) {
	llm := &FakeLLM{On: func(CompletionRequest) (CompletionResult, error) {
		return CompletionResult{}, errors.New("down")
	}}
	_, hit, err := guardianAgent{llm: llm}.Assess(context.Background(), Snapshot{}, Event{})
	if hit {
		t.Fatal("基础设施故障不得臆造打断")
	}
	if !errors.Is(err, ErrLLM) {
		t.Fatalf("期望 ErrLLM，得到 %v", err)
	}
}

func TestGuardianHitWithEmptyReasonIsSchemaError(t *testing.T) {
	llm := &FakeLLM{On: func(CompletionRequest) (CompletionResult, error) {
		return StructuredOf(emergencyWire{Hit: true}), nil
	}}
	_, hit, err := guardianAgent{llm: llm}.Assess(context.Background(), Snapshot{}, Event{})
	var se *SchemaError
	if hit || !errors.As(err, &se) {
		t.Fatalf("命中但缺 reason 应为 SchemaError，得到 hit=%v err=%v", hit, err)
	}
}

func TestNewGuardianWiring(t *testing.T) {
	// 覆盖公开构造器 NewGuardian（其余守护测试直接构造 guardianAgent）。
	g := NewGuardian(&FakeLLM{On: func(CompletionRequest) (CompletionResult, error) {
		return StructuredOf(emergencyWire{Hit: true, Reason: "危急"}), nil
	}})
	ei, hit, err := g.Assess(context.Background(), Snapshot{}, Event{Kind: "vital", Data: "胸痛"})
	if err != nil || !hit || ei.Reason == "" {
		t.Fatalf("NewGuardian 装配异常：ei=%+v hit=%v err=%v", ei, hit, err)
	}
}
