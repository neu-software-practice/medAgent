package agent

import (
	"fmt"
	"sync/atomic"

	"medagent/internal/ai"
)

// ── agent 测试公用 helper：把工具调用序列脚本化喂给引擎 ──

var tcSeq int64

// tc 构造一个唯一 ID 的工具调用 AssistantTurn。
func tc(name string, args any) ai.AssistantTurn {
	id := fmt.Sprintf("c%d", atomic.AddInt64(&tcSeq, 1))
	return ai.ToolCallTurn(id, name, args)
}

func askT(q string) ai.AssistantTurn { return tc("ask_patient", map[string]any{"question": q}) }
func orderTestT() ai.AssistantTurn {
	return tc("order_test", map[string]any{"items": []string{"血常规"}})
}
func queryDrugT(names ...string) ai.AssistantTurn {
	return tc("query_drug_spec", map[string]any{"names": names})
}
func purchaseT(orders ...map[string]any) ai.AssistantTurn {
	return tc("purchase_drug", map[string]any{"orders": orders})
}
func finishAdviceT(name, advice string) ai.AssistantTurn {
	return tc("finish", map[string]any{"diagnosis": map[string]any{"name": name}, "plan": "ADVICE_ONLY", "advice": advice})
}
func finishMedT(name, advice string, meds ...map[string]any) ai.AssistantTurn {
	return tc("finish", map[string]any{"diagnosis": map[string]any{"name": name}, "plan": "MEDICATION", "medications": meds, "advice": advice})
}
func referT(reason, advice string) ai.AssistantTurn {
	return tc("refer", map[string]any{"reason": reason, "advice": advice})
}

// chatScript 返回按调用序号产出预设 turn 的 FakeChat（越界则 finish 兜底）。
func chatScript(turns ...ai.AssistantTurn) *ai.FakeChat {
	var i int64 = -1
	return &ai.FakeChat{OnChat: func(ai.ChatRequest) (ai.AssistantTurn, error) {
		n := int(atomic.AddInt64(&i, 1))
		if n >= len(turns) {
			return finishAdviceT("结束", "结束"), nil
		}
		return turns[n], nil
	}}
}

// chatFn 返回按调用序号（从 1 计）走自定义逻辑的 FakeChat（可注入错误）。
func chatFn(fn func(n int) (ai.AssistantTurn, error)) *ai.FakeChat {
	var i int64
	return &ai.FakeChat{OnChat: func(ai.ChatRequest) (ai.AssistantTurn, error) {
		return fn(int(atomic.AddInt64(&i, 1)))
	}}
}

// noGuardian / guardianHit / guardianErr 是守护 fake（走 Complete + emergency schema）。
func noGuardian() *ai.FakeLLM {
	return &ai.FakeLLM{On: func(ai.CompletionRequest) (ai.CompletionResult, error) {
		return ai.StructuredOf(map[string]any{"hit": false}), nil
	}}
}

func guardianHit(reason string) *ai.FakeLLM {
	return &ai.FakeLLM{On: func(ai.CompletionRequest) (ai.CompletionResult, error) {
		return ai.StructuredOf(map[string]any{"hit": true, "reason": reason}), nil
	}}
}

func guardianErr() *ai.FakeLLM {
	return &ai.FakeLLM{On: func(ai.CompletionRequest) (ai.CompletionResult, error) {
		return ai.CompletionResult{}, ai.ErrLLM
	}}
}

// svcChat 用脚本引擎建 Service，守护关闭（用于纯流程测试）。
func svcChat(chat *ai.FakeChat) *Service {
	return newService(Config{DisableGuardian: true}, ai.NewEngine(chat), ai.NewGuardian(noGuardian()))
}

// svcGuarded 用脚本引擎 + 指定守护建 Service，守护开启。
func svcGuarded(chat *ai.FakeChat, guard *ai.FakeLLM) *Service {
	return newService(Config{}, ai.NewEngine(chat), ai.NewGuardian(guard))
}
