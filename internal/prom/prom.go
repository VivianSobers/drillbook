// Package prom asks Prometheus whether an alert is firing.
package prom

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

type Client struct {
	base string
	http *http.Client
}

func New(base string) *Client {
	return &Client{base: strings.TrimRight(base, "/"), http: &http.Client{Timeout: 30 * time.Second}}
}

// Selector builds ALERTS{alertname=...,alertstate="firing",...} with labels sorted.
func Selector(alert string, labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := []string{"alertname=" + quote(alert), `alertstate="firing"`}
	for _, k := range keys {
		parts = append(parts, k+"="+quote(labels[k]))
	}
	return "ALERTS{" + strings.Join(parts, ",") + "}"
}

func quote(v string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return `"` + r.Replace(v) + `"`
}

// Firing reports whether any ALERTS series with these labels is firing now.
func (c *Client) Firing(ctx context.Context, alert string, labels map[string]string) (bool, error) {
	u := c.base + "/api/v1/query?query=" + url.QueryEscape(Selector(alert, labels))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("prometheus query: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var r struct {
		Status string `json:"status"`
		Error  string `json:"error"`
		Data   struct {
			Result []json.RawMessage `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return false, fmt.Errorf("prometheus query: %w", err)
	}
	if r.Status != "success" {
		return false, fmt.Errorf("prometheus query: %s", r.Error)
	}
	return len(r.Data.Result) > 0, nil
}

// Ready returns nil when Prometheus answers /-/ready with 200.
func (c *Client) Ready(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/-/ready", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("prometheus not ready: HTTP %d", resp.StatusCode)
	}
	return nil
}
