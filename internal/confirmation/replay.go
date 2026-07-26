package confirmation

import (
	"crypto/sha256"
	"errors"
	"sync"
	"time"
)

var ErrReplayedToken=errors.New("confirmation token already used")

type ReplayGuard struct{
	mu sync.Mutex
	used map[[32]byte]time.Time
	now func()time.Time
}
func NewReplayGuard()*ReplayGuard{return &ReplayGuard{used:make(map[[32]byte]time.Time),now:time.Now}}
func(g *ReplayGuard)Consume(token string)error{
	digest:=sha256.Sum256([]byte(token));now:=g.now()
	g.mu.Lock();defer g.mu.Unlock()
	for key,usedAt:=range g.used{if now.Sub(usedAt)>15*time.Minute{delete(g.used,key)}}
	if _,exists:=g.used[digest];exists{return ErrReplayedToken}
	g.used[digest]=now
	return nil
}
