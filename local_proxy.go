package main

import (
	"bytes"
	"io"
	"net/http"
)

// LAN clients reach Frigate's API only through the edge: port 5000 is published
// on the Frigate host's loopback and the app has no Frigate credential (edge
// keeps the JWT). This mirrors the relay proxy for LAN, with the same allowlist
// and the same client token the app already uses for LAN live view (issue #9).
const localProxyMaxBody = 256 * 1024

var localProxyHeaders = []string{"Accept", "Content-Type", "Range", "If-None-Match", "If-Modified-Since"}

func (r *relayController) serveLocalProxy(w http.ResponseWriter, request *http.Request) {
	if !r.authorizeLocalClient(request) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	target := request.URL.RequestURI()
	if !validProxyPath(target) {
		http.NotFound(w, request)
		return
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, localProxyMaxBody+1))
	if err != nil || len(body) > localProxyMaxBody {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request too large"})
		return
	}
	upstream, err := http.NewRequestWithContext(
		request.Context(), request.Method, r.manager.cfg.frigateURL+target, bytes.NewReader(body))
	if err != nil {
		http.Error(w, "bad proxy target", http.StatusBadRequest)
		return
	}
	for _, name := range localProxyHeaders {
		if value := request.Header.Get(name); value != "" {
			upstream.Header.Set(name, value)
		}
	}
	response, err := r.manager.client.Do(upstream)
	if err != nil {
		http.Error(w, "frigate unavailable", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	for _, name := range []string{
		"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "ETag",
	} {
		if value := response.Header.Get(name); value != "" {
			w.Header().Set(name, value)
		}
	}
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, response.Body)
}
