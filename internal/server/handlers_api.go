package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/mikepea/home-cnc/internal/api"
	"github.com/mikepea/home-cnc/internal/store"
)

// handlePoll is the agent long-poll endpoint. It returns a pending command
// immediately if one exists, otherwise blocks up to PollHold waiting for one,
// then returns 204 if still nothing.
func (s *Server) handlePoll(w http.ResponseWriter, r *http.Request, dev store.Device) {
	if cmd, ok := s.nextCommand(w, r, dev.ID); ok {
		writeCommand(w, cmd)
		return
	}

	ch, cancel := s.notif.subscribe(dev.ID)
	defer cancel()

	select {
	case <-ch:
		// A command was just enqueued; fall through to re-query.
	case <-time.After(s.cfg.PollHold):
		// Hold elapsed with no work.
	case <-r.Context().Done():
		return // client hung up
	}

	if cmd, ok := s.nextCommand(w, r, dev.ID); ok {
		writeCommand(w, cmd)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// nextCommand fetches the next pending command, writing an error response and
// returning ok=false on a real failure.
func (s *Server) nextCommand(w http.ResponseWriter, r *http.Request, deviceID string) (store.Command, bool) {
	c, err := s.store.NextPendingCommand(r.Context(), deviceID)
	if errors.Is(err, store.ErrNotFound) {
		return store.Command{}, false
	}
	if err != nil {
		s.log.Error("next command", "err", err)
		http.Error(w, "server error", http.StatusInternalServerError)
		return store.Command{}, false
	}
	return c, true
}

func writeCommand(w http.ResponseWriter, c store.Command) {
	out := api.Command{
		ID:        c.ID,
		DeviceID:  c.DeviceID,
		Type:      c.Type,
		ExpiresAt: c.ExpiresAt.Unix(),
		Signature: c.Signature,
	}
	writeJSON(w, http.StatusOK, out)
}

// handleAck records the result the agent reports for a command.
func (s *Server) handleAck(w http.ResponseWriter, r *http.Request, dev store.Device) {
	cmdID := r.PathValue("id")

	var req api.AckRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, "bad request body", http.StatusBadRequest)
		return
	}

	var status string
	switch req.Status {
	case "done":
		status = "acked"
	case "failed":
		status = "failed"
	default:
		http.Error(w, "status must be 'done' or 'failed'", http.StatusBadRequest)
		return
	}

	err := s.store.AckCommand(r.Context(), dev.ID, cmdID, status, req.Result)
	if errors.Is(err, store.ErrNotFound) {
		// Already acked, expired, or not this device's command.
		http.Error(w, "command not pending", http.StatusConflict)
		return
	}
	if err != nil {
		s.log.Error("ack command", "err", err)
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	s.log.Info("command acked", "device", dev.Name, "cmd", cmdID, "status", status)
	w.WriteHeader(http.StatusNoContent)
}

// handlePubkey exposes the server's command-signing public key (informational;
// the agent's trust anchor is the copy baked into its config, not this).
func (s *Server) handlePubkey(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"public_key": s.signer.PubBase64()})
}

// handleHealthz is a readiness probe: it confirms the datastore is reachable.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if _, err := s.store.CountUsers(r.Context()); err != nil {
		http.Error(w, "unhealthy", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte("ok"))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
