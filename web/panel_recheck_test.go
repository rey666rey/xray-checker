package web

import (
	"bytes"
	"strings"
	"testing"
)

func TestInlinePanelRecheck(t *testing.T) {
	for _, public := range []bool{false, true} {
		var out bytes.Buffer
		if err := RenderIndex(&out, PageData{IsPublic: public, ShowServerDetails: !public}); err != nil {
			t.Fatal(err)
		}
		html := out.String()
		visible := strings.Contains(html, `class="monitor-recheck"`)
		if visible == public {
			t.Fatalf("public=%v inline recheck visibility=%v", public, visible)
		}
		if strings.Contains(html, `class="monitor-host-actions"`) {
			t.Fatal("old full-width recheck row remains")
		}
		if !public {
			rowStart := strings.Index(html, `class="monitor-host-address-row"`)
			bottomStart := strings.Index(html, `class="monitor-host-bottom"`)
			if rowStart < 0 || bottomStart < rowStart || !strings.Contains(html[rowStart:bottomStart], `class="monitor-recheck"`) {
				t.Fatal("recheck icon is not next to address controls")
			}
			copyIndex := strings.Index(html[rowStart:bottomStart], `class="monitor-copy-ip"`)
			recheckIndex := strings.Index(html[rowStart:bottomStart], `class="monitor-recheck"`)
			if copyIndex < 0 || recheckIndex < copyIndex {
				t.Fatal("copy must immediately precede recheck in address row")
			}
			recheck := html[strings.Index(html, `class="monitor-recheck"`):]
			recheck = recheck[:strings.Index(recheck, `</button>`)]
			if strings.Contains(recheck, `<span`) || !strings.Contains(recheck, `<svg width="14" height="14"`) {
				t.Fatal("recheck must be icon-only")
			}
			for _, want := range []string{`@click.stop="recheckProxy(proxy)"`, `:disabled="!canRecheckProxy(proxy)"`, `:aria-busy="proxy.rechecking"`, `'Перепроверить узел ' + proxy.name`} {
				if !strings.Contains(html, want) {
					t.Fatalf("missing recheck control %q", want)
				}
			}
		}
		if !strings.Contains(html, `if (!this.canRecheckProxy(proxy)) return;`) {
			t.Fatal("missing recheck request guard")
		}
	}
}
