// Package consultlog 提供一个 logging ai.LLMClient 装饰器：按 visitID 把每次诊疗
// 经过的所有 LLM 调用记成完整审计流，每次诊疗写一个 JSONL 文件，按 visitID 可寻。
// 装饰器与决策层解耦，日志失败绝不影响诊疗。
package consultlog

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"
)

type ctxKey struct{}

// WithVisitID 把一次诊疗的 visitID 绑到 ctx，该 ctx 下的所有 Complete 都归到这次诊疗。
func WithVisitID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// VisitID 取出 ctx 上的 visitID，不存在返回 ""。
func VisitID(ctx context.Context) string {
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}

// NewVisitID 生成形如 20260624-153012-a1b2c3d4 的 visitID（本地时间 + 4 字节随机后缀）。
func NewVisitID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return time.Now().Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
}
