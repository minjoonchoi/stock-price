package collector

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProductionCollectorCodeDoesNotReferenceRemovedPriceProvider(t *testing.T) {
	removedProvider := strings.Join([]string{"sto", "oq"}, "")
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || strings.HasSuffix(name, "_test.go") || !strings.HasSuffix(name, ".go") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", name, err)
		}
		content := strings.ToLower(string(raw))
		if strings.Contains(strings.ToLower(name), removedProvider) || strings.Contains(content, removedProvider) {
			t.Fatalf("production collector file %s still references removed price provider", name)
		}
	}
}
