package server

import "sync"

// notifier wakes up long-poll waiters the moment a command is enqueued for a
// device, so a lock/halt lands in ~1s instead of waiting out the poll hold.
type notifier struct {
	mu   sync.Mutex
	subs map[string]map[chan struct{}]struct{}
}

func newNotifier() *notifier {
	return &notifier{subs: make(map[string]map[chan struct{}]struct{})}
}

// subscribe registers a waiter for deviceID. The returned cancel func must be
// called (defer) to deregister.
func (n *notifier) subscribe(deviceID string) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	n.mu.Lock()
	if n.subs[deviceID] == nil {
		n.subs[deviceID] = make(map[chan struct{}]struct{})
	}
	n.subs[deviceID][ch] = struct{}{}
	n.mu.Unlock()

	return ch, func() {
		n.mu.Lock()
		if m := n.subs[deviceID]; m != nil {
			delete(m, ch)
			if len(m) == 0 {
				delete(n.subs, deviceID)
			}
		}
		n.mu.Unlock()
	}
}

// notify wakes all current waiters for deviceID (non-blocking).
func (n *notifier) notify(deviceID string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for ch := range n.subs[deviceID] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
