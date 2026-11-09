package cmd

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

type deadlineTransport struct {
	testing *testing.T
	want    time.Duration
	calls   int
}

func (d *deadlineTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	d.calls++
	deadline, ok := r.Context().Deadline()
	remaining := time.Until(deadline)
	if !ok || remaining <= 0 || remaining > d.want || remaining < d.want-time.Second {
		d.testing.Errorf("request %s deadline remaining = %v, present = %v; want %v", r.URL.Path, remaining, ok, d.want)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{"version":"1.24.0"}`)),
		Request:    r,
	}, nil
}

func TestClientRequestTimeout(t *testing.T) {
	for _, tc := range []struct {
		name    string
		seconds int
		want    time.Duration
	}{
		{"default", 0, 30 * time.Second},
		{"configured", 7, 7 * time.Second},
		{"negative uses default", -1, 30 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configureSafetyServer(t, "https://example.invalid")
			f, err := os.OpenFile(configPath, os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			_, err = fmt.Fprintf(f, "timeout = %d\n", tc.seconds)
			closeErr := f.Close()
			if err != nil || closeErr != nil {
				t.Fatalf("write config: %v, close: %v", err, closeErr)
			}

			transport := &deadlineTransport{testing: t, want: tc.want}
			previous := http.DefaultTransport
			http.DefaultTransport = transport
			t.Cleanup(func() { http.DefaultTransport = previous })

			client, err := getClient(&cobra.Command{})
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := client.GetIssue("owner", "repo", 1); err != nil {
				t.Fatal(err)
			}
			resp, err := apiDo(&cobra.Command{}, http.MethodGet, "version", nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if transport.calls < 3 {
				t.Fatalf("got %d requests; want version check, issue read and raw API request", transport.calls)
			}
		})
	}
}
