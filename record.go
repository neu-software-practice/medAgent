package medagent

import (
	"fmt"
	"strings"
	"time"
)

func nowSec() time.Time { return time.Now().Truncate(time.Second) }

// renderHistory 把先前会话纪要渲染成喂模型的历史文本。
func renderHistory(prior []SessionRecord) string {
	if len(prior) == 0 {
		return ""
	}
	var b strings.Builder
	for i, r := range prior {
		visit := "复诊"
		if r.Initial {
			visit = "初诊"
		}
		fmt.Fprintf(&b, "· 第%d次(%s, %s):\n", i+1, visit, r.StartedAt.Format("2006-01-02"))
		if r.Outcome != nil {
			if r.Outcome.Diagnosis != nil {
				fmt.Fprintf(&b, "    诊断: %s\n", r.Outcome.Diagnosis.Name)
			}
			for _, m := range r.Outcome.Medications {
				fmt.Fprintf(&b, "    处方: %s %s\n", m.Name, m.Dosage)
			}
			if r.Outcome.Advice != "" {
				fmt.Fprintf(&b, "    医嘱: %s\n", r.Outcome.Advice)
			}
		}
		for _, tn := range r.Turns {
			if tn.Kind == "patient" || tn.Kind == "doctor" {
				fmt.Fprintf(&b, "    [%s] %s: %s\n", tn.At.Format("15:04:05"), tn.Kind, tn.Text)
			}
		}
	}
	return b.String()
}
