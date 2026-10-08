package prom

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func server(t *testing.T, status int, body string, gotQuery *string) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if gotQuery != nil {
			*gotQuery = r.URL.Query().Get("query")
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.Close)
	return s
}

const firing = `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"__name__":"ALERTS","alertname":"KubeNodeNotReady","alertstate":"firing","node":"w"},"value":[1,"1"]}]}}`
const empty = `{"status":"success","data":{"resultType":"vector","result":[]}}`

func TestFiringBuildsSelectorWithSortedEscapedLabels(t *testing.T) {
	var q string
	s := server(t, 200, firing, &q)
	ok, err := New(s.URL).Firing(context.Background(), "KubeNodeNotReady", map[string]string{"node": "w", "a": `say "hi"\now`})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("want firing")
	}
	want := `ALERTS{alertname="KubeNodeNotReady",alertstate="firing",a="say \"hi\"\\now",node="w"}`
	if q != want {
		t.Errorf("query\n got %s\nwant %s", q, want)
	}
}

func TestFiringEmptyResultIsNotFiring(t *testing.T) {
	ok, err := New(server(t, 200, empty, nil).URL).Firing(context.Background(), "X", map[string]string{"a": "b"})
	if err != nil || ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestFiringHTTPErrorIsError(t *testing.T) {
	_, err := New(server(t, 500, "boom", nil).URL).Firing(context.Background(), "X", nil)
	if err == nil {
		t.Fatal("want error on HTTP 500")
	}
}

func TestFiringAPIErrorStatusIsError(t *testing.T) {
	_, err := New(server(t, 200, `{"status":"error","errorType":"bad_data","error":"parse error"}`, nil).URL).Firing(context.Background(), "X", nil)
	if err == nil {
		t.Fatal("want error on status=error")
	}
}

func TestReadyChecksEndpoint(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/-/ready" {
			w.WriteHeader(404)
		}
	}))
	defer s.Close()
	if err := New(s.URL).Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := New(s.URL + "/nope").Ready(context.Background()); err == nil {
		t.Fatal("want error for 404")
	}
}
