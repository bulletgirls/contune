package risk

// DefaultFeatures는 기본 특징 표다(05 §6.2). 새 특징은 표에 한 줄을 더한다.
// 규칙 모델 가중치는 표 순서대로 1, 1, 1, 2, 2, 1, 2, 1.
func DefaultFeatures() []FeatureDef {
	return []FeatureDef{
		{Name: "cpu_max", Title: "최대 서버 CPU 사용률", Metric: "host.cpu.pct", HostAgg: "max", TimeAgg: "avg", Source: "metrics", RuleLo: 70, RuleHi: 95, RuleWeight: 1},
		{Name: "mem_max", Title: "최대 메모리 사용률", Metric: "host.mem.used_pct", HostAgg: "max", TimeAgg: "avg", Source: "metrics", RuleLo: 80, RuleHi: 95, RuleWeight: 1},
		{Name: "hosts_hot", Title: "CPU 70% 초과 서버 수", Metric: "host.cpu.pct", HostAgg: "count_ge", Threshold: 70, TimeAgg: "max", Source: "metrics", RuleLo: 1, RuleHi: 5, RuleWeight: 1},
		{Name: "queue", Title: "대기 큐", Metric: "biz.order.queue", HostAgg: "sum", TimeAgg: "max", Source: "metrics", RuleLo: 500, RuleHi: 2000, RuleWeight: 2},
		{Name: "p99", Title: "p99 응답 시간", Metric: "biz.order.latency_p99_ms", HostAgg: "max", TimeAgg: "max", Source: "metrics", RuleLo: 50, RuleHi: 200, RuleWeight: 2},
		{Name: "lag", Title: "처리량 부족(유입 − 처리)", Metric: "biz.order.in_tps", Metric2: "biz.order.tps", HostAgg: "sum", TimeAgg: "avg", Source: "metrics", RuleLo: 0, RuleHi: 1000, RuleWeight: 1},
		{Name: "log_errors", Title: "오류 로그 수", Metric: "log.error.count", HostAgg: "sum", TimeAgg: "sum", Source: "metrics", RuleLo: 0, RuleHi: 10, RuleWeight: 2},
		{Name: "runs_failed", Title: "실패한 배치 실행 수", Source: "runs_failed", RuleLo: 0, RuleHi: 2, RuleWeight: 1},
	}
}

// featureTitle은 기본 특징 표의 표시 이름이다. 표에 없으면 이름 그대로.
func featureTitle(name string) string {
	for _, f := range DefaultFeatures() {
		if f.Name == name {
			return f.Title
		}
	}
	return name
}

func featureNames(feats []FeatureDef) []string {
	out := make([]string, len(feats))
	for i, f := range feats {
		out[i] = f.Name
	}
	return out
}
