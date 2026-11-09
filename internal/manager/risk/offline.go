package risk

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"text/tabwriter"
	"time"

	"eum/internal/manager/store/metricsql"
	"eum/internal/manager/store/statesql"
)

// TrainOffline은 dataDir의 두 DB로 학습해 보고서를 w에 찍고 model.json을 쓴다(eum risk train).
// 매니저의 주기 학습과 같은 train을 돈다.
func TrainOffline(dataDir string, cfg Config, w io.Writer) error {
	ms, err := metricsql.Open(filepath.Join(dataDir, "metrics.db"))
	if err != nil {
		return fmt.Errorf("metrics.db: %w", err)
	}
	defer ms.Close()
	ss, err := statesql.Open(filepath.Join(dataDir, "state.db"))
	if err != nil {
		return fmt.Errorf("state.db: %w", err)
	}
	defer ss.Close()
	e, err := New(Deps{Metrics: ms, State: ss, Cfg: cfg, DataDir: dataDir, Now: time.Now})
	if err != nil {
		return err
	}
	res, err := e.train(context.Background())
	if err != nil {
		return err
	}
	return printReport(w, res, e.modelPath())
}

func printReport(w io.Writer, res trainResult, path string) error {
	rep := res.Report
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "모드\t%s\n", res.Mode)
	if res.Note != "" {
		fmt.Fprintf(tw, "노트\t%s\n", res.Note)
	}
	fmt.Fprintf(tw, "모델\t%s\n", rep.Kind)
	fmt.Fprintf(tw, "학습 시각\t%s\n", time.UnixMilli(rep.TrainedAt).Format(time.RFC3339))
	fmt.Fprintf(tw, "창\t%d\n", rep.Windows)
	fmt.Fprintf(tw, "양성 창\t%d\n", rep.Positives)
	fmt.Fprintf(tw, "장애\t%d\n", rep.Incidents)
	fmt.Fprintf(tw, "AUC\t%.3f\n", rep.AUC)
	names := make([]string, 0, len(rep.Weights))
	for n := range rep.Weights {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(tw, "가중치 %s\t%.4f\n", n, rep.Weights[n])
	}
	if res.Mode == "model" && rep.Kind != "llm" {
		fmt.Fprintf(tw, "model.json\t%s\n", path)
	}
	return tw.Flush()
}
