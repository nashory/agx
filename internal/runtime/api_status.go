package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

func (s *Service) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.Status())
}

func (s *Service) handleShutdown(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]bool{"shuttingDown": true})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	}()
}

func (s *Service) handleEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	events, cancel := s.bus.Subscribe()
	defer cancel()
	fmt.Fprintf(w, "event: runtime.status\n")
	fmt.Fprintf(w, "id: 0000000000000000\n")
	data, _ := json.Marshal(Event{ID: "0000000000000000", Type: "runtime.status", Timestamp: s.started, Seq: 0, Payload: mustJSON(s.Status())})
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case event := <-events:
			data, err := json.Marshal(event)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\n", event.Type)
			fmt.Fprintf(w, "id: %s\n", event.ID)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}
