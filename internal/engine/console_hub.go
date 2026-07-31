package engine

import (
	"bytes"
	"regexp"
	"strings"
	"sync"
	"time"
)

const provisionLogLimit = 5000

var provisionSecretPattern = regexp.MustCompile(
	`(?i)\b(password|token|secret|api[_-]?key)\b\s*[:=]\s*\S+`,
)

type provisionLogHub struct {
	mu          sync.Mutex
	frames      []consoleFrame
	subscribers map[chan consoleFrame]struct{}
	done        chan struct{}
	closed      bool
}

type hubLineFrameWriter struct {
	hub     *provisionLogHub
	stream  string
	phase   string
	pending []byte
}

func (w *hubLineFrameWriter) Write(value []byte) (int, error) {
	w.pending = append(w.pending, value...)
	for {
		index := bytes.IndexByte(w.pending, '\n')
		if index < 0 {
			break
		}
		w.emit(string(w.pending[:index]))
		w.pending = w.pending[index+1:]
	}
	return len(value), nil
}

func (w *hubLineFrameWriter) flush() {
	if len(w.pending) > 0 {
		w.emit(string(w.pending))
		w.pending = nil
	}
}

func (w *hubLineFrameWriter) emit(message string) {
	w.hub.emit(consoleFrame{
		Stream: w.stream, Phase: w.phase, Message: strings.TrimSuffix(message, "\r"),
		ObservedAt: time.Now().UTC(),
	})
}

func newProvisionLogHub() *provisionLogHub {
	return &provisionLogHub{
		frames:      make([]consoleFrame, 0, 256),
		subscribers: make(map[chan consoleFrame]struct{}),
		done:        make(chan struct{}),
	}
}

func (h *provisionLogHub) emit(frame consoleFrame) {
	frame.Message = sanitizeProvisionLog(frame.Message)
	if frame.Message == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.frames = append(h.frames, frame)
	if len(h.frames) > provisionLogLimit {
		copy(h.frames, h.frames[len(h.frames)-provisionLogLimit:])
		h.frames = h.frames[:provisionLogLimit]
	}
	for subscriber := range h.subscribers {
		select {
		case subscriber <- frame:
		default:
		}
	}
}

func (h *provisionLogHub) subscribe() ([]consoleFrame, <-chan consoleFrame, <-chan struct{}, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	snapshot := append([]consoleFrame(nil), h.frames...)
	channel := make(chan consoleFrame, 2048)
	if !h.closed {
		h.subscribers[channel] = struct{}{}
	}
	cancel := func() {
		h.mu.Lock()
		delete(h.subscribers, channel)
		h.mu.Unlock()
	}
	return snapshot, channel, h.done, cancel
}

func (h *provisionLogHub) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	close(h.done)
	h.subscribers = make(map[chan consoleFrame]struct{})
}

func sanitizeProvisionLog(message string) string {
	message = strings.Map(func(character rune) rune {
		if character == '\t' || character >= ' ' {
			return character
		}
		return -1
	}, message)
	message = provisionSecretPattern.ReplaceAllString(message, "$1=[REDACTED]")
	return strings.TrimSpace(message)
}

func (s *Server) startProvisionLog(serverID string) *provisionLogHub {
	hub := newProvisionLogHub()
	s.consoleMu.Lock()
	s.provisionLogs[serverID] = hub
	s.consoleMu.Unlock()
	return hub
}

func (s *Server) provisionLog(serverID string) *provisionLogHub {
	s.consoleMu.RLock()
	defer s.consoleMu.RUnlock()
	return s.provisionLogs[serverID]
}

func (s *Server) finishProvisionLog(serverID string, hub *provisionLogHub) {
	hub.close()
	time.AfterFunc(10*time.Minute, func() {
		s.consoleMu.Lock()
		if s.provisionLogs[serverID] == hub {
			delete(s.provisionLogs, serverID)
		}
		s.consoleMu.Unlock()
	})
}
