package adguardfake_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

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

// at is the time of the query log line i seconds before the newest.
func at(i int) string {
	return time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC).Add(-time.Duration(i) * time.Second).Format(time.RFC3339Nano)
}

type page struct {
	Data   []map[string]any `json:"data"`
	Oldest string           `json:"oldest"`
}

func querylog(t *testing.T, a *adguardfake.AdGuard, query string) page {
	t.Helper()
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/control/querylog?"+query, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("querylog?%s = %d %s", query, w.Code, w.Body)
	}
	var p page
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func names(p page) []string {
	var out []string
	for _, e := range p.Data {
		out = append(out, e["question"].(map[string]any)["name"].(string))
	}
	return out
}

// sparse is a log of 10 lines, a second apart, where only lines 7 and 8
// are 198.18.1.9's.
func sparse(t *testing.T, scanCap int) *adguardfake.AdGuard {
	t.Helper()
	var log []map[string]any
	for i := range 10 {
		client := "198.18.1.2"
		if i == 7 || i == 8 {
			client = "198.18.1.9"
		}
		log = append(log, map[string]any{
			"time": at(i), "client": client, "reason": "NotFilteredNotFound",
			"question": map[string]any{"name": fmt.Sprintf("site%d.example", i), "type": "A"},
		})
	}
	a, err := adguardfake.New("../testdata/baseline", adguardfake.Options{QueryLog: log, ScanCap: scanCap})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestARequestWithoutOffsetScansNoFurtherThanTheScanCapAndStillCarriesACursor(t *testing.T) {
	a := sparse(t, 5)

	first := querylog(t, a, `search="198.18.1.9"&limit=5`)
	if len(first.Data) != 0 || first.Oldest != at(4) {
		t.Fatalf("first page = %v oldest %q, want none and the 5th line's time", names(first), first.Oldest)
	}
	second := querylog(t, a, `search="198.18.1.9"&limit=5&older_than=`+first.Oldest)
	if got := names(second); !slices.Equal(got, []string{"site7.example", "site8.example"}) || second.Oldest != at(8) {
		t.Errorf("second page = %v oldest %q, want lines 7 and 8, the last one's time", got, second.Oldest)
	}
}

func TestOffsetLiftsTheScanCap(t *testing.T) {
	a := sparse(t, 5)

	got := querylog(t, a, `search="198.18.1.9"&limit=5&offset=0`)

	if !slices.Equal(names(got), []string{"site7.example", "site8.example"}) {
		t.Errorf("page = %v, want lines 7 and 8", names(got))
	}
}

func TestOffsetSkipsMatchingRows(t *testing.T) {
	a := sparse(t, 0)

	if got := querylog(t, a, `search="198.18.1.9"&limit=5&offset=1`); !slices.Equal(names(got), []string{"site8.example"}) {
		t.Errorf("offset 1 = %v, want line 8", names(got))
	}
	if got := querylog(t, a, `search="198.18.1.9"&limit=5&offset=2`); len(got.Data) != 0 || got.Oldest != "" {
		t.Errorf("offset 2 = %v oldest %q, want nothing and no cursor", names(got), got.Oldest)
	}
}

func TestTheEndOfTheLogIsAnEmptyOldestOnTheCallAfterTheLastRows(t *testing.T) {
	a := sparse(t, 0)

	last := querylog(t, a, `search="198.18.1.9"&limit=5`)
	if len(last.Data) != 2 || last.Oldest != at(8) {
		t.Fatalf("last page = %v oldest %q, want two rows and the last one's time", names(last), last.Oldest)
	}
	end := querylog(t, a, `search="198.18.1.9"&limit=5&older_than=`+last.Oldest)
	if len(end.Data) != 0 || end.Oldest != "" {
		t.Errorf("after the last page = %v oldest %q, want nothing and no cursor", names(end), end.Oldest)
	}
}

func TestAnAnonymizingAdGuardLogsMaskedClientAddresses(t *testing.T) {
	a, err := adguardfake.New("../testdata/anonymized", adguardfake.Options{})
	if err != nil {
		t.Fatal(err)
	}

	if got := querylog(t, a, `search="198.18.1.102"`); len(got.Data) != 0 {
		t.Errorf("the full address found %v", names(got))
	}
	if got := querylog(t, a, `search="198.18.0.0"&limit=1`); len(got.Data) != 1 {
		t.Errorf("the masked address found %v, want the newest line", names(got))
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/control/querylog/config", nil))
	var cfg struct {
		AnonymizeClientIP bool `json:"anonymize_client_ip"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &cfg); err != nil || !cfg.AnonymizeClientIP {
		t.Errorf("config = %s, want anonymize_client_ip on", w.Body)
	}
}
