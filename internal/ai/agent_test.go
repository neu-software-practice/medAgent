package ai

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// 测试用的最小 intent 类型。
type fakeIntent struct {
	OK bool `json:"ok"`
}

func (f fakeIntent) Validate() error {
	if !f.OK {
		return fmt.Errorf("not ok")
	}
	return nil
}

func run(ctx context.Context, llm LLMClient) (fakeIntent, error) {
	return runStructured[fakeIntent](ctx, llm, "test", "sys", OutputSchema{Name: "t"}, Snapshot{})
}

func TestRunStructuredHappyPath(t *testing.T) {
	llm := &FakeLLM{On: func(CompletionRequest) (CompletionResult, error) {
		return StructuredOf(fakeIntent{OK: true}), nil
	}}
	out, err := run(context.Background(), llm)
	if err != nil || !out.OK {
		t.Fatalf("期望成功，得到 out=%+v err=%v", out, err)
	}
}

func TestRunStructuredWrapsLLMError(t *testing.T) {
	llm := &FakeLLM{On: func(CompletionRequest) (CompletionResult, error) {
		return CompletionResult{}, errors.New("boom")
	}}
	_, err := run(context.Background(), llm)
	if !errors.Is(err, ErrLLM) {
		t.Fatalf("期望 ErrLLM，得到 %v", err)
	}
}

func TestRunStructuredCanceledContext(t *testing.T) {
	llm := &FakeLLM{On: func(CompletionRequest) (CompletionResult, error) {
		return StructuredOf(fakeIntent{OK: true}), nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := run(ctx, llm); err == nil {
		t.Fatal("期望 ctx 取消错误")
	}
}

func TestRunStructuredMidFlightCancel(t *testing.T) {
	// ctx 在 Complete 执行期间被取消（模拟急症抢占/超时）应以原始 ctx 错误上抛，
	// 不归类为 ErrLLM（spec §8）。
	ctx, cancel := context.WithCancel(context.Background())
	llm := &FakeLLM{On: func(CompletionRequest) (CompletionResult, error) {
		cancel()
		return CompletionResult{}, context.Canceled
	}}
	_, err := run(ctx, llm)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("期望 context.Canceled，得到 %v", err)
	}
	if errors.Is(err, ErrLLM) {
		t.Fatal("mid-flight 取消不应被归类为 ErrLLM")
	}
}

func TestRunStructuredRetriesThenSucceeds(t *testing.T) {
	n := 0
	llm := &FakeLLM{On: func(CompletionRequest) (CompletionResult, error) {
		n++
		if n == 1 {
			return StructuredOf(fakeIntent{OK: false}), nil // 首次非法
		}
		return StructuredOf(fakeIntent{OK: true}), nil
	}}
	out, err := run(context.Background(), llm)
	if err != nil || !out.OK {
		t.Fatalf("期望 K 次内自纠，得到 out=%+v err=%v", out, err)
	}
	if n != 2 {
		t.Fatalf("期望调用 2 次，实际 %d", n)
	}
}

func TestRunStructuredExhaustsRetries(t *testing.T) {
	calls := 0
	llm := &FakeLLM{On: func(CompletionRequest) (CompletionResult, error) {
		calls++
		return StructuredOf(fakeIntent{OK: false}), nil
	}}
	_, err := run(context.Background(), llm)
	var se *SchemaError
	if !errors.As(err, &se) {
		t.Fatalf("期望 *SchemaError，得到 %v", err)
	}
	if se.Attempts != SchemaRetryMax+1 || calls != SchemaRetryMax+1 {
		t.Fatalf("重试次数不符：attempts=%d calls=%d", se.Attempts, calls)
	}
}

func TestRunStructuredUnmarshalFailureRetries(t *testing.T) {
	// 合法 JSON 但形状错误（数组无法反序列化进结构体）触发 json.Unmarshal 失败路径（spec §8 的另一触发器）。
	calls := 0
	llm := &FakeLLM{On: func(CompletionRequest) (CompletionResult, error) {
		calls++
		return StructuredOf([]int{1, 2, 3}), nil
	}}
	_, err := run(context.Background(), llm)
	var se *SchemaError
	if !errors.As(err, &se) {
		t.Fatalf("期望 *SchemaError，得到 %v", err)
	}
	if calls != SchemaRetryMax+1 {
		t.Fatalf("期望重试 %d 次，实际 %d", SchemaRetryMax+1, calls)
	}
}
