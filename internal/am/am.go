// Package am creates and deletes Alertmanager silences and reads which
// receivers an alert was routed to.
package am

import (
	"bytes"
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

type matcher struct {
	Name    string `json:"name"`
	Value   string `json:"value"`
	IsRegex bool   `json:"isRegex"`
	IsEqual bool   `json:"isEqual"`
}

// CreateSilence silences exactly this alert on exactly these labels until `until`.
func (c *Client) CreateSilence(ctx context.Context, alert string, labels map[string]string, until time.Time, createdBy, comment string) (string, error) {
	ms := []matcher{{Name: "alertname", Value: alert, IsEqual: true}}
	for k, v := range labels {
		ms = append(ms, matcher{Name: k, Value: v, IsEqual: true})
	}
	body, err := json.Marshal(map[string]any{
		"matchers":  ms,
		"startsAt":  time.Now().UTC().Format(time.RFC3339),
		"endsAt":    until.UTC().Format(time.RFC3339),
		"createdBy": createdBy,
		"comment":   comment,
	})
	if err != nil {
		return "", err
	}
	var out struct {
		SilenceID string `json:"silenceID"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/v2/silences", body, &out); err != nil {
		return "", fmt.Errorf("create silence: %w", err)
	}
	return out.SilenceID, nil
}

// DeleteSilence expires a silence. A silence that is already gone is not an error.
func (c *Client) DeleteSilence(ctx context.Context, id string) error {
	err := c.do(ctx, http.MethodDelete, "/api/v2/silence/"+url.PathEscape(id), nil, nil)
	if he, ok := err.(httpError); ok && he.code == http.StatusNotFound {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete silence %s: %w", id, err)
	}
	return nil
}

// Receivers returns the sorted, de-duplicated receivers of every alert in
// Alertmanager matching the name and labels, including silenced and inhibited
// ones. found is false when Alertmanager has no such alert.
func (c *Client) Receivers(ctx context.Context, alert string, labels map[string]string) ([]string, bool, error) {
	q := url.Values{}
	for _, k := range []string{"active", "silenced", "inhibited", "unprocessed"} {
		q.Set(k, "true")
	}
	q.Add("filter", fmt.Sprintf("alertname=%q", alert))
	for k, v := range labels {
		q.Add("filter", fmt.Sprintf("%s=%q", k, v))
	}
	var alerts []struct {
		Receivers []struct {
			Name string `json:"name"`
		} `json:"receivers"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v2/alerts?"+q.Encode(), nil, &alerts); err != nil {
		return nil, false, fmt.Errorf("list alerts: %w", err)
	}
	seen := map[string]bool{}
	var out []string
	for _, a := range alerts {
		for _, r := range a.Receivers {
			if !seen[r.Name] {
				seen[r.Name] = true
				out = append(out, r.Name)
			}
		}
	}
	sort.Strings(out)
	return out, len(alerts) > 0, nil
}

type httpError struct {
	code int
	body string
}

func (e httpError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.code, e.body) }

func (c *Client) do(ctx context.Context, method, path string, body []byte, out any) error {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return httpError{resp.StatusCode, strings.TrimSpace(string(b))}
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}
