package upgrade

import (
	"context"
	"errors"
	"net/http"
	"sync"
)

var ErrMutationsFrozen = errors.New("mutating requests are frozen for an upgrade checkpoint")

type MutationGate struct {
	mu      sync.Mutex
	frozen  bool
	active  int
	changed chan struct{}
}

func NewMutationGate() *MutationGate {
	return &MutationGate{changed: make(chan struct{})}
}

func (g *MutationGate) Middleware(next http.Handler) http.Handler {
	if next == nil {
		next = http.NotFoundHandler()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isMutatingRequest(r) || isUpgradeControlRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		done, err := g.begin()
		if err != nil {
			w.Header().Set("Content-Type", "application/problem+json")
			w.Header().Set("Retry-After", "5")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("{\"error\":\"upgrade_maintenance\",\"message\":\"mutating operations are paused while an upgrade checkpoint is active\"}\n"))
			return
		}
		defer done()
		next.ServeHTTP(w, r)
	})
}

func (g *MutationGate) Freeze(ctx context.Context) error {
	if g == nil {
		return errors.New("HTTP mutation gate is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	g.mu.Lock()
	if !g.frozen {
		g.frozen = true
		g.notifyLocked()
	}
	for g.active > 0 {
		changed := g.changed
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
		g.mu.Lock()
	}
	g.mu.Unlock()
	return nil
}

func (g *MutationGate) Resume() {
	if g == nil {
		return
	}
	g.mu.Lock()
	if g.frozen {
		g.frozen = false
		g.notifyLocked()
	}
	g.mu.Unlock()
}

func (g *MutationGate) Frozen() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.frozen
}

func (g *MutationGate) begin() (func(), error) {
	if g == nil {
		return nil, errors.New("HTTP mutation gate is unavailable")
	}
	g.mu.Lock()
	if g.frozen {
		g.mu.Unlock()
		return nil, ErrMutationsFrozen
	}
	g.active++
	g.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			g.active--
			g.notifyLocked()
			g.mu.Unlock()
		})
	}, nil
}

func (g *MutationGate) notifyLocked() {
	close(g.changed)
	g.changed = make(chan struct{})
}

func isMutatingRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return false
	default:
		return true
	}
}

func isUpgradeControlRequest(r *http.Request) bool {
	if r == nil || r.Method != http.MethodPost {
		return false
	}
	switch r.URL.Path {
	case "/api/v1/system/upgrade/checkpoint", "/api/v1/system/upgrade/promoted", "/api/v1/system/upgrade/aborted":
		return true
	default:
		return false
	}
}

type TaskPauser interface {
	Pause(context.Context) error
	Resume()
}

type DatabaseWriteFreezer interface {
	FreezeWrites(context.Context) error
	ResumeWrites()
}

type Coordinator struct {
	Requests *MutationGate
	Tasks    TaskPauser
	Database DatabaseWriteFreezer

	mu       sync.Mutex
	quiesced bool
}

// Quiesce closes the public mutation gate first, drains complete task
// executions second, and finally waits for every SQLite writer.
func (c *Coordinator) Quiesce(ctx context.Context) (bool, error) {
	if c == nil || c.Requests == nil || c.Tasks == nil || c.Database == nil {
		return false, errors.New("upgrade quiescence dependencies are required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.quiesced {
		return false, nil
	}
	if err := c.Requests.Freeze(ctx); err != nil {
		c.Requests.Resume()
		return false, err
	}
	if err := c.Tasks.Pause(ctx); err != nil {
		c.Tasks.Resume()
		c.Requests.Resume()
		return false, err
	}
	if err := c.Database.FreezeWrites(ctx); err != nil {
		c.Database.ResumeWrites()
		c.Tasks.Resume()
		c.Requests.Resume()
		return false, err
	}
	c.quiesced = true
	return true, nil
}

func (c *Coordinator) Resume() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.quiesced {
		return
	}
	c.Database.ResumeWrites()
	c.Tasks.Resume()
	c.Requests.Resume()
	c.quiesced = false
}

func (c *Coordinator) Quiesced() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.quiesced
}
