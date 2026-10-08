package am

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"testing"
	"time"
)

func TestCreateSilenceSendsExactMatchers(t *testing.T) {
	var body map[string]any
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v2/silences" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		_, _ = w.Write([]byte(`{"silenceID":"abc-123"}`))
	}))
	defer s.Close()
	until := time.Date(2026, 10, 8, 13, 0, 0, 0, time.UTC)
	id, err := New(s.URL).CreateSilence(context.Background(), "KubeNodeNotReady", map[string]string{"node": "w"}, until, "drillbook", "drill kubelet-stopped")
	if err != nil {
		t.Fatal(err)
	}
	if id != "abc-123" {
		t.Errorf("id = %q", id)
	}
	ms := body["matchers"].([]any)
	if len(ms) != 2 {
		t.Fatalf("matchers = %v", ms)
	}
	var names []string
	for _, m := range ms {
		mm := m.(map[string]any)
		names = append(names, mm["name"].(string)+"="+mm["value"].(string))
		if mm["isRegex"] != false || mm["isEqual"] != true {
			t.Errorf("matcher must be exact equality: %v", mm)
		}
	}
	sort.Strings(names)
	if !reflect.DeepEqual(names, []string{"alertname=KubeNodeNotReady", "node=w"}) {
		t.Errorf("matchers = %v", names)
	}
	if body["endsAt"] != "2026-10-08T13:00:00Z" || body["createdBy"] != "drillbook" || body["comment"] != "drill kubelet-stopped" {
		t.Errorf("body = %v", body)
	}
}

func TestDeleteSilenceToleratesAlreadyGone(t *testing.T) {
	codes := map[string]int{"/api/v2/silence/ok": 200, "/api/v2/silence/gone": 404, "/api/v2/silence/err": 500}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method %s", r.Method)
		}
		w.WriteHeader(codes[r.URL.Path])
	}))
	defer s.Close()
	c := New(s.URL)
	if err := c.DeleteSilence(context.Background(), "ok"); err != nil {
		t.Error(err)
	}
	if err := c.DeleteSilence(context.Background(), "gone"); err != nil {
		t.Errorf("404 means already expired, want nil: %v", err)
	}
	if err := c.DeleteSilence(context.Background(), "err"); err == nil {
		t.Error("500 must be an error")
	}
}

// recorded from Alertmanager v0.28 /api/v2/alerts, trimmed.
const alertsResp = `[
 {"labels":{"alertname":"KubeNodeNotReady","node":"w","severity":"warning"},"receivers":[{"name":"null"}],"status":{"state":"suppressed","silencedBy":["abc"],"inhibitedBy":[]}},
 {"labels":{"alertname":"KubeNodeNotReady","node":"w","severity":"warning","prometheus":"monitoring/kps"},"receivers":[{"name":"platform"},{"name":"null"}],"status":{"state":"active"}}
]`

func TestReceiversIncludesSilencedAndFilters(t *testing.T) {
	var q map[string][]string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q = r.URL.Query()
		_, _ = w.Write([]byte(alertsResp))
	}))
	defer s.Close()
	got, found, err := New(s.URL).Receivers(context.Background(), "KubeNodeNotReady", map[string]string{"node": "w"})
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("want found")
	}
	if !reflect.DeepEqual(got, []string{"null", "platform"}) {
		t.Errorf("receivers = %v, want sorted unique [null platform]", got)
	}
	for _, k := range []string{"active", "silenced", "inhibited", "unprocessed"} {
		if q[k][0] != "true" {
			t.Errorf("query %s = %v", k, q[k])
		}
	}
	f := q["filter"]
	sort.Strings(f)
	if !reflect.DeepEqual(f, []string{`alertname="KubeNodeNotReady"`, `node="w"`}) {
		t.Errorf("filters = %v", f)
	}
}

func TestReceiversNotFound(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`[]`)) }))
	defer s.Close()
	got, found, err := New(s.URL).Receivers(context.Background(), "X", nil)
	if err != nil || found || len(got) != 0 {
		t.Fatalf("got=%v found=%v err=%v", got, found, err)
	}
}
