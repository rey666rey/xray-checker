package networksource

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSessionRetentionPreservesCurrentAndUnrelatedFiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), "network-sources", "router")
	current := strings.Repeat("f", 32)
	for i, name := range []string{strings.Repeat("a", 32), strings.Repeat("b", 32), strings.Repeat("c", 32), current, "legacy"} {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
		at := time.Unix(int64(i), 0)
		_ = os.Chtimes(p, at, at)
	}
	if err := PruneSessions(filepath.Join(root, current, "results.json"), 2); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, strings.Repeat("a", 32))); !os.IsNotExist(err) {
		t.Fatal("old session not pruned")
	}
	for _, name := range []string{strings.Repeat("b", 32), strings.Repeat("c", 32), current, "legacy"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatal("retained path removed", name)
		}
	}
}
