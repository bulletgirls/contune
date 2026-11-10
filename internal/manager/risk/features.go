package risk

// DefaultFeatures는 기본 특징 표다(05 §6.2). 새 특징은 표에 한 줄을 더한다.
// 규칙 모델 가중치는 표 순서대로 1, 1, 1, 2, 2, 1, 2, 1. Check는 화면 "확인할 것"의 질문이다.
func DefaultFeatures() []FeatureDef {
	return []FeatureDef{
		{Name: "cpu_max", Title: "최대 서버 CPU 사용률", Metric: "host.cpu.pct", HostAgg: "max", TimeAgg: "avg", Source: "metrics", RuleLo: 70, RuleHi: 95, RuleWeight: 1,
			Check: "어느 프로세스가 CPU를 쓰는가 · 배치 시간대와 겹치는가"},
		{Name: "mem_max", Title: "최대 메모리 사용률", Metric: "host.mem.used_pct", HostAgg: "max", TimeAgg: "avg", Source: "metrics", RuleLo: 80, RuleHi: 95, RuleWeight: 1,
			Check: "메모리가 계속 늘고 있는가(누수) · 같은 서버의 다른 요인도 올랐나"},
		{Name: "hosts_hot", Title: "CPU 70% 초과 서버 수", Metric: "host.cpu.pct", HostAgg: "count_ge", Threshold: 70, TimeAgg: "max", Source: "metrics", RuleLo: 1, RuleHi: 5, RuleWeight: 1,
			Check: "여러 서버가 함께 올랐다면 유입 증가 같은 공통 원인부터"},
		{Name: "queue", Title: "대기 큐", Metric: "biz.order.queue", HostAgg: "sum", TimeAgg: "max", Source: "metrics", RuleLo: 500, RuleHi: 2000, RuleWeight: 2,
			Check: "유입이 늘었나, 처리가 줄었나(처리량 부족과 같이 본다)"},
		{Name: "p99", Title: "p99 응답 시간", Metric: "biz.order.latency_p99_ms", HostAgg: "max", TimeAgg: "max", Source: "metrics", RuleLo: 50, RuleHi: 200, RuleWeight: 2,
			Check: "느린 서버가 한 대인가 전부인가 · CPU·대기 큐와 같이 올랐나"},
		{Name: "lag", Title: "처리량 부족(유입 − 처리)", Metric: "biz.order.in_tps", Metric2: "biz.order.tps", HostAgg: "sum", TimeAgg: "avg", Source: "metrics", RuleLo: 0, RuleHi: 1000, RuleWeight: 1,
			Check: "처리가 유입을 못 따라간다 · 처리 서버 상태와 대기 큐 추이"},
		{Name: "log_errors", Title: "오류 로그 수", Metric: "log.error.count", HostAgg: "sum", TimeAgg: "sum", Source: "metrics", RuleLo: 0, RuleHi: 10, RuleWeight: 2,
			Check: "새로운 오류 문구인가, 늘 나던 것인가", Where: "log"},
		{Name: "runs_failed", Title: "실패한 배치 실행 수", Source: "runs_failed", RuleLo: 0, RuleHi: 2, RuleWeight: 1,
			Check: "재시도가 남았는가 · 후행 배치가 막혔는가", Where: "run"},
	}
}

// featureDef는 기본 특징 표에서 name의 정의다.
func featureDef(feats []FeatureDef, name string) (FeatureDef, bool) {
	for _, f := range feats {
		if f.Name == name {
			return f, true
		}
	}
	return FeatureDef{}, false
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
