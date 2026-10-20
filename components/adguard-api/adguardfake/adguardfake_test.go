package adguardfake_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/itsKontra/ddash/components/adguard-api/adguardfake"
)

func post(a *adguardfake.AdGuard, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
	return w
}

func TestDeleteRemovesAPersistentClientByName(t *testing.T) {
	a, err := adguardfake.New("../testdata/baseline", adguardfake.Options{})
	if err != nil {
		t.Fatal(err)
	}

	if w := post(a, "/control/clients/delete", `{"name": "AndroidTV"}`); w.Code != http.StatusOK {
		t.Fatalf("delete = %d %s", w.Code, w.Body)
	}

	if a.PersistentClient("AndroidTV") != nil {
		t.Errorf("the client is still there")
	}
	if a.PersistentClient("Huawei Inv") == nil {
		t.Errorf("another client went too")
	}
}

func TestDeleteOfAnUnknownOrUnnamedClientIsRefused(t *testing.T) {
	a, err := adguardfake.New("../testdata/baseline", adguardfake.Options{})
	if err != nil {
		t.Fatal(err)
	}

	for _, body := range []string{`{"name": "Nobody"}`, `{"name": ""}`, `{`} {
		if w := post(a, "/control/clients/delete", body); w.Code != http.StatusBadRequest {
			t.Errorf("delete %s = %d, want 400", body, w.Code)
		}
	}
	if len(a.Clients()) != 6 {
		t.Errorf("a refused delete removed a client: %v", a.Clients())
	}
}
