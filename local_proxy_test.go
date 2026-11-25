package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newLocalProxy(t *testing.T, frigateURL string) (*relayController, *httptest.ResponseRecorder, *http.Request) {
	t.Helper()
	identity := deriveIdentity("local-proxy-root")
	r := newRelayController("", "", identity, &streamManager{
		cfg: config{frigateURL: frigateURL}, client: http.DefaultClient,
	})
	return r, httptest.NewRecorder(), nil
}

func TestLocalProxyRejectsUnpairedAndPublicCallers(t *testing.T) {
	r, _, _ := newLocalProxy(t, "http://127.0.0.1:1")
	identity := deriveIdentity("local-proxy-root")
	clientToken := identity.ClientToken

	cases := []struct {
		name       string
		remoteAddr string
		header     string
	}{
		{"no token", "10.0.0.5:1234", ""},
		{"wrong token", "10.0.0.5:1234", "Bearer nope"},
		{"public address", "8.8.8.8:1234", "Bearer " + clientToken},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "http://edge/api/events", nil)
		req.RemoteAddr = tc.remoteAddr
		if tc.header != "" {
			req.Header.Set("Authorization", tc.header)
		}
		rec := httptest.NewRecorder()
		r.serveLocalProxy(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s: got %d, want 401", tc.name, rec.Code)
		}
	}
}

func TestLocalProxyForwardsAllowedPaths(t *testing.T) {
	var seen []string
	frigate := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.RequestURI()+" range="+r.Header.Get("Range"))
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Range", "bytes 0-1/2")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("ok"))
	}))
	defer frigate.Close()
	r, _, _ := newLocalProxy(t, frigate.URL)
	token := "Bearer " + r.identity.ClientToken

	for _, target := range []string{"/api/events?limit=5", "/vod/event/e1/master.m3u8"} {
		req := httptest.NewRequest(http.MethodGet, "http://edge"+target, nil)
		req.RemoteAddr = "10.0.0.5:1234"
		req.Header.Set("Authorization", token)
		req.Header.Set("Range", "bytes=0-1")
		rec := httptest.NewRecorder()
		r.serveLocalProxy(rec, req)
		if rec.Code != http.StatusPartialContent || rec.Body.String() != "ok" {
			t.Fatalf("%s: got %d %q", target, rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Content-Range") != "bytes 0-1/2" {
			t.Fatalf("%s: Content-Range not forwarded", target)
		}
	}
	want := []string{
		"GET /api/events?limit=5 range=bytes=0-1",
		"GET /vod/event/e1/master.m3u8 range=bytes=0-1",
	}
	if strings.Join(seen, "|") != strings.Join(want, "|") {
		t.Fatalf("frigate saw %v, want %v", seen, want)
	}
}

func TestLocalProxyBlocksPathsOutsideAllowlist(t *testing.T) {
	r, _, _ := newLocalProxy(t, "http://127.0.0.1:1")
	for _, target := range []string{"/api/login", "/api/config/save", "/health", "/api/../etc/passwd"} {
		req := httptest.NewRequest(http.MethodGet, "http://edge"+target, nil)
		req.RemoteAddr = "10.0.0.5:1234"
		req.Header.Set("Authorization", "Bearer "+r.identity.ClientToken)
		rec := httptest.NewRecorder()
		r.serveLocalProxy(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: got %d, want 404", target, rec.Code)
		}
	}
}

func TestLocalProxyRejectsLargeBodyAndUpstreamFailure(t *testing.T) {
	r, _, _ := newLocalProxy(t, "http://127.0.0.1:1")
	big := httptest.NewRequest(http.MethodPost, "http://edge/api/events/x",
		io.NopCloser(strings.NewReader(strings.Repeat("a", localProxyMaxBody+1))))
	big.RemoteAddr = "10.0.0.5:1234"
	big.Header.Set("Authorization", "Bearer "+r.identity.ClientToken)
	rec := httptest.NewRecorder()
	r.serveLocalProxy(rec, big)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("big body: got %d, want 413", rec.Code)
	}

	small := httptest.NewRequest(http.MethodGet, "http://edge/api/events", nil)
	small.RemoteAddr = "10.0.0.5:1234"
	small.Header.Set("Authorization", "Bearer "+r.identity.ClientToken)
	rec = httptest.NewRecorder()
	r.serveLocalProxy(rec, small)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("upstream down: got %d, want 502", rec.Code)
	}
}
