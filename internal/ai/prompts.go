package ai

import "encoding/json"

const promptInterview = `你是无人医院的 AI 导诊医生，正在问诊阶段采集主观信息。基于【就诊快照】中已采集的信息、对话历史和患者最新消息，判断信息是否足以交给决策环节。
规则：
- 一次只问一个最具鉴别诊断价值的问题，不要堆砌多个问题。
- 你只负责采集与追问，不下诊断（诊断由后续环节负责）。
- 若【编排反馈】指出"需补充"的项，优先围绕这些项提问。
- 当你判断主观信息已足够时，在 advance.subjective 中给出本轮采集到的结构化主诉/病史/体征，并在 reply 给患者一句简短过场。
- 语言为简体中文，口语化、患者能听懂。
- 这是概念性项目，按你的医学判断行事，不要套用固定的危险信号清单。
按给定 JSON schema 输出。`

const promptTriage = `你是无人医院的收敛判断器。基于【就诊快照】的全部信息（主观信息、检验结果、对话摘要）做三选一决策。
原则：主观信息优先，检验是最后手段。
- 能确诊：decision=CONFIRM，给出 diagnosis 的 name、basis、confidence(0~1)。
- 还缺主观信息：decision=INTERVIEW，在 missing_subjective 指明还需采集哪些项。
- 主观信息已问尽仍无法确诊：decision=TEST，必须 subjective_exhausted=true，给出 reason 说明为何必须借助检验，并在 test_items 列出检验项目。
- 若【编排反馈】指出上次意图被拒（如自证不成立），据此改选。
- 置信度由你自评。这是概念性项目，按你的医学判断行事，不套用固定危险信号清单。
按给定 JSON schema 输出。`

const promptTreatment = `你是无人医院的处置决策器，已经确诊（见【就诊快照】）。做四选一处置，并无条件写入医嘱 advice。
- advice 恒须给出（休息/饮水/观察/风险提示等），且不因患者态度而改变。
- MEDICATION 用药：在 medications 给出 name、dosage、schedule。
- TREATMENT 治疗：在 required_capability 给出用于比对本院能力清单的能力标识。
- ADVICE_ONLY 仅医嘱：只给 advice。
- REFERRAL 转诊：在 referral_reason 说明原因。
- 若【就诊快照】有患者拒绝记录，在 advice 注明相应执行状态与风险（如"未购药，注意…"）。
语言为简体中文。按给定 JSON schema 输出。`

const promptGuardian = `你是无人医院的急症守护，与主流程并行运行，实时读取全量信息流。基于【就诊快照】与【最新事件】，判断当前是否出现需要立即打断、转去急诊的危急情况。
- 若命中：hit=true，并在 reason 简述危急依据。
- 若未命中：hit=false。
- 这是概念性项目，按你的临床判断行事，不套用固定危险信号清单；宁严勿松，但不要无依据地打断。
按给定 JSON schema 输出。`

var schemaInterview = OutputSchema{Name: "interview", JSON: json.RawMessage(`{
  "type": "object",
  "properties": {
    "reply": {"type": "string"},
    "advance": {"type": ["object", "null"], "properties": {"subjective": {"type": "object"}}, "required": ["subjective"]}
  },
  "required": ["reply"]
}`)}

var schemaTriage = OutputSchema{Name: "triage_decide", JSON: json.RawMessage(`{
  "type": "object",
  "properties": {
    "decision": {"type": "string", "enum": ["CONFIRM", "INTERVIEW", "TEST"]},
    "diagnosis": {"type": ["object", "null"], "properties": {"name": {"type": "string"}, "basis": {"type": "string"}, "confidence": {"type": "number"}}},
    "missing_subjective": {"type": "array", "items": {"type": "string"}},
    "subjective_exhausted": {"type": "boolean"},
    "reason": {"type": "string"},
    "test_items": {"type": "array", "items": {"type": "string"}}
  },
  "required": ["decision"]
}`)}

var schemaTreatment = OutputSchema{Name: "treatment_plan", JSON: json.RawMessage(`{
  "type": "object",
  "properties": {
    "plan": {"type": "string", "enum": ["MEDICATION", "TREATMENT", "ADVICE_ONLY", "REFERRAL"]},
    "advice": {"type": "string"},
    "medications": {"type": "array", "items": {"type": "object", "properties": {"name": {"type": "string"}, "dosage": {"type": "string"}, "schedule": {"type": "string"}}}},
    "required_capability": {"type": "string"},
    "referral_reason": {"type": "string"}
  },
  "required": ["plan", "advice"]
}`)}

var schemaEmergency = OutputSchema{Name: "emergency_interrupt", JSON: json.RawMessage(`{
  "type": "object",
  "properties": {
    "hit": {"type": "boolean"},
    "reason": {"type": "string"}
  },
  "required": ["hit"]
}`)}
