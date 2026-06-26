package agent

import "errors"

var (
	ErrSessionNotFound = errors.New("medagent: session not found")
	ErrSessionClosed   = errors.New("medagent: session already completed")
	ErrWrongStep       = errors.New("medagent: call does not match current step")
	ErrUpstream        = errors.New("medagent: upstream LLM call failed")
	ErrModelOutput     = errors.New("medagent: model output invalid")
)
