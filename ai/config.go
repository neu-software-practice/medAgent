package ai

const (
	// InterviewRawTurns 是 buildMessages 保留的最近原文轮数，更早折叠成 digest。
	InterviewRawTurns = 6
	// SchemaRetryMax 是 schema-invalid 时 agent 内部重试次数（K）。
	SchemaRetryMax = 2
)
