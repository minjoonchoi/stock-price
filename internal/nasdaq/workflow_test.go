package nasdaq

import (
	"os"
	"strings"
	"testing"
)

func TestNasdaqWorkflowFilesDeclareSchedulesDispatchAndCommitPaths(t *testing.T) {
	tests := []struct {
		path      string
		cron      string
		command   string
		commitMsg string
		dataPath  string
	}{
		{
			path:      "../../.github/workflows/collect-nasdaq-splits.yml",
			cron:      `cron: "10 21 * * *"`,
			command:   "go run ./scripts/collect-nasdaq-splits",
			commitMsg: "chore(data): update nasdaq split calendar",
			dataPath:  "data/nasdaq/splits",
		},
		{
			path:      "../../.github/workflows/collect-nasdaq-dividends.yml",
			cron:      `cron: "20 21 * * *"`,
			command:   "go run ./scripts/collect-nasdaq-dividends",
			commitMsg: "chore(data): update nasdaq dividend calendar",
			dataPath:  "data/nasdaq/dividends",
		},
		{
			path:      "../../.github/workflows/collect-nasdaq-screener.yml",
			cron:      `cron: "30 21 * * *"`,
			command:   "go run ./scripts/collect-nasdaq-screener",
			commitMsg: "chore(data): update nasdaq screener stocks",
			dataPath:  "data/nasdaq/screener",
		},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			raw, err := os.ReadFile(tt.path)
			if err != nil {
				t.Fatalf("ReadFile() error = %v", err)
			}
			workflow := string(raw)
			for _, expected := range []string{
				tt.cron,
				"workflow_dispatch:",
				"permissions:",
				"contents: write",
				"timeout-minutes: 60",
				"concurrency:",
				tt.command,
				tt.commitMsg,
				tt.dataPath,
			} {
				if !strings.Contains(workflow, expected) {
					t.Fatalf("workflow %s missing %q", tt.path, expected)
				}
			}
		})
	}
}
