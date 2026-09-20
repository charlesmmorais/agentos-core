package core

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// record.create is the sole write adapter. The destination MUST atomically bind
// the stable ID to immutable payload+digest and retain a queryable receipt.
func executeRecord(ctx context.Context, a Intent, allowWrite bool) (*WriteReceipt, bool, error) {
	token := os.Getenv("AGENTOS_WRITE_TOKEN")
	if token == "" {
		return nil, false, errors.New("AGENTOS_WRITE_TOKEN is required")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 3 * time.Second, ResponseHeaderTimeout: 5 * time.Second, MaxConnsPerHost: 1}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("write adapter redirect blocked") }}
	endpoint := strings.TrimRight(a.Endpoint, "/") + "/v1/records/" + a.ID
	request := func(method string, body []byte) (*WriteReceipt, bool, error) {
		req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, false, errors.New("invalid write request")
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return nil, false, errors.New("write transport failed")
		}
		defer resp.Body.Close()
		if method == "GET" && resp.StatusCode == 404 {
			return nil, true, nil
		}
		if resp.StatusCode != 200 && !(method == "PUT" && resp.StatusCode == 201) {
			return nil, false, errors.New("destination did not confirm operation")
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, 4097))
		if err != nil || len(b) > 4096 {
			return nil, false, errors.New("invalid receipt size")
		}
		var receipt WriteReceipt
		if json.Unmarshal(b, &receipt) != nil || !a.validReceipt(&receipt) {
			return nil, false, errors.New("receipt identity or digest mismatch")
		}
		return &receipt, false, nil
	}
	receipt, absent, err := request("GET", nil)
	if err != nil || !absent || !allowWrite {
		return receipt, absent, err
	}
	if ctx.Err() != nil {
		return nil, false, ctx.Err()
	}
	b, _ := json.Marshal(struct {
		ActionID string        `json:"action_id"`
		Digest   string        `json:"digest"`
		Payload  RecordPayload `json:"payload"`
	}{a.ID, a.Digest, a.Payload})
	return request("PUT", b)
}
