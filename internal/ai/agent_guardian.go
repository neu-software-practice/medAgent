package ai

import (
	"context"
	"encoding/json"
	"fmt"
)

// guardianAgent 是急症守护：并行读全量信息流，命中即打断。单次判断，不内部重试。
type guardianAgent struct{ llm LLMClient }

// emergencyWire 是守护输出的线格式（hit + reason）。
type emergencyWire struct {
	Hit    bool   `json:"hit"`
	Reason string `json:"reason"`
}

func (a guardianAgent) Assess(ctx context.Context, s Snapshot, ev Event) (EmergencyInterrupt, bool, error) {
	if err := ctx.Err(); err != nil {
		return EmergencyInterrupt{}, false, err
	}
	msgs := append(buildMessages(s), Message{Role: "user", Content: renderEvent(ev)})
	res, err := a.llm.Complete(ctx, CompletionRequest{System: promptGuardian, Messages: msgs, Schema: schemaEmergency})
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return EmergencyInterrupt{}, false, ctxErr
		}
		return EmergencyInterrupt{}, false, fmt.Errorf("%w: guardian: %v", ErrLLM, err)
	}
	var w emergencyWire
	if err := json.Unmarshal(res.Structured, &w); err != nil {
		return EmergencyInterrupt{}, false, &SchemaError{Agent: "guardian", Attempts: 1, LastRaw: res.Raw, Cause: err}
	}
	if !w.Hit {
		return EmergencyInterrupt{}, false, nil
	}
	ei := EmergencyInterrupt{Reason: w.Reason}
	if err := ei.Validate(); err != nil {
		return EmergencyInterrupt{}, false, &SchemaError{Agent: "guardian", Attempts: 1, LastRaw: res.Raw, Cause: err}
	}
	return ei, true, nil
}

func renderEvent(ev Event) string {
	return fmt.Sprintf("【最新事件】类型 %s：%v", ev.Kind, ev.Data)
}
