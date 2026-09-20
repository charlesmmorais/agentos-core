package core

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
)

func (c *Controller) Handler(token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		expected := sha256.Sum256([]byte("Bearer " + token))
		actual := sha256.Sum256([]byte(r.Header.Get("Authorization")))
		if len(token) < 32 || subtle.ConstantTimeCompare(expected[:], actual[:]) != 1 {
			http.Error(w, "unauthorized", 401)
			return
		}
		if r.Method == "GET" && r.URL.Path == "/v1/state" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(c.Snapshot())
			return
		}
		if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/v1/artifacts/") {
			cycle, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/v1/artifacts/"))
			if err != nil {
				http.Error(w, "invalid cycle", 400)
				return
			}
			b, err := c.Artifact(cycle)
			if err != nil {
				http.Error(w, "artifact unavailable or invalid", 404)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write(b)
			return
		}
		if r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/v1/control/") {
			action := strings.TrimPrefix(r.URL.Path, "/v1/control/")
			if err := c.Control(action); err != nil {
				http.Error(w, "control rejected", 409)
				return
			}
			w.WriteHeader(204)
			return
		}
		if r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/v1/actions/") {
			var request struct {
				ID      string        `json:"id"`
				Digest  string        `json:"digest"`
				Payload RecordPayload `json:"payload"`
			}
			d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
			d.DisallowUnknownFields()
			if d.Decode(&request) != nil || d.Decode(&struct{}{}) != io.EOF {
				http.Error(w, "invalid action request", 400)
				return
			}
			a, err := c.ActionControl(strings.TrimPrefix(r.URL.Path, "/v1/actions/"), request.ID, request.Digest, request.Payload)
			if err != nil {
				http.Error(w, "action control rejected", 409)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(a)
			return
		}
		http.Error(w, "not found", 404)
	})
}
