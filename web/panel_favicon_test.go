package web

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPanelFaviconCacheRevision(t *testing.T) {
	names := []string{"favicon-96x96.png", "favicon.svg", "favicon.ico"}
	var contents [][]byte
	hash := sha256.New()
	for _, name := range names {
		data, err := staticFiles.ReadFile("static/" + name)
		if err != nil {
			t.Fatal(err)
		}
		hash.Write(data)
		contents = append(contents, data)
	}
	revision := fmt.Sprintf("%x", hash.Sum(nil))[:12]
	for _, public := range []bool{false, true} {
		var out bytes.Buffer
		if err := RenderIndex(&out, PageData{IsPublic: public}); err != nil {
			t.Fatal(err)
		}
		for i, name := range names {
			url := "./static/" + name + "?v=" + revision
			if !strings.Contains(out.String(), `href="`+url+`"`) {
				t.Fatalf("public=%v missing versioned icon %s", public, name)
			}
			request := httptest.NewRequest("GET", "/static/"+name+"?v="+revision, nil)
			response := httptest.NewRecorder()
			StaticHandler().ServeHTTP(response, request)
			if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), contents[i]) {
				t.Fatalf("%s response=%d; incorrect favicon bytes", name, response.Code)
			}
			if !strings.HasPrefix(response.Header().Get("Content-Type"), "image/") {
				t.Fatalf("invalid favicon content type: %s", response.Header().Get("Content-Type"))
			}
		}
	}
}
