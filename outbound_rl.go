package outbound_rl

import (
	"context"
	"sync"
	"time"

	agent "github.com/nuln/agent-core"
)

func init() {
	agent.RegisterPluginConfigSpec(agent.PluginConfigSpec{
		PluginName:  "outbound_rl",
		PluginType:  "pipe",
		Description: "Outbound rate limiter — throttles replies to prevent flooding downstream dialogs",
		Fields: []agent.ConfigField{
			{Key: "max_replies_per_min", Description: "Max outbound replies per minute per session", Default: "30", Type: agent.ConfigFieldInt},
		},
	})

	agent.RegisterPipe("outbound_rl", 950, func(_ agent.PipeContext) agent.Pipe {
		return NewOutboundRateLimiter(30, time.Minute)
	})
}

// OutboundRateLimiter throttles outbound replies using a per-session token bucket.
type OutboundRateLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*outboundBucket
	maxCount int
	window   time.Duration
}

type outboundBucket struct {
	timestamps []time.Time
}

// NewOutboundRateLimiter creates an outbound rate limiter.
func NewOutboundRateLimiter(maxCount int, window time.Duration) *OutboundRateLimiter {
	rl := &OutboundRateLimiter{
		buckets:  make(map[string]*outboundBucket),
		maxCount: maxCount,
		window:   window,
	}
	go rl.cleanup()
	return rl
}

func (r *OutboundRateLimiter) Handle(ctx context.Context, d agent.Dialog, msg *agent.Message) bool {
	if !r.allow(msg.SessionKey) {
		_ = d.Reply(ctx, msg.ReplyCtx, "⚠️ Too many replies. Please slow down.")
		return true
	}
	return false
}

func (r *OutboundRateLimiter) allow(sessionKey string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	b := r.buckets[sessionKey]
	if b == nil {
		b = &outboundBucket{}
		r.buckets[sessionKey] = b
	}
	cutoff := now.Add(-r.window)
	filtered := b.timestamps[:0]
	for _, t := range b.timestamps {
		if t.After(cutoff) {
			filtered = append(filtered, t)
		}
	}
	b.timestamps = filtered
	if len(b.timestamps) >= r.maxCount {
		return false
	}
	b.timestamps = append(b.timestamps, now)
	return true
}

func (r *OutboundRateLimiter) cleanup() {
	tick := time.NewTicker(5 * time.Minute)
	defer tick.Stop()
	for range tick.C {
		r.mu.Lock()
		for k, b := range r.buckets {
			if len(b.timestamps) == 0 {
				delete(r.buckets, k)
			}
		}
		r.mu.Unlock()
	}
}
