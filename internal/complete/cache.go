package complete

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// CacheEnv lists environment variables that change completion results and
// are therefore part of the cache key.
var CacheEnv = []string{"KUBECONFIG", "HELM_NAMESPACE", "DOCKER_HOST", "DOCKER_CONTEXT"}

// Cache stores completion results on disk. Entries are never served as the
// only answer once stale; callers show them while refreshing.
type Cache struct {
	Dir string
}

type cacheEntry struct {
	Time   time.Time `json:"t"`
	Result Result    `json:"r"`
}

func (c *Cache) path(backend string, req Request) string {
	h := sha256.New()
	h.Write([]byte(backend + "\x00" + req.Key() + "\x00" + req.Line))
	for _, e := range CacheEnv {
		h.Write([]byte("\x00" + e + "=" + os.Getenv(e)))
	}
	return filepath.Join(c.Dir, hex.EncodeToString(h.Sum(nil)[:12])+".json")
}

// Get returns a cached result and when it was stored.
func (c *Cache) Get(backend string, req Request) (Result, time.Time, bool) {
	if c == nil || c.Dir == "" {
		return Result{}, time.Time{}, false
	}
	data, err := os.ReadFile(c.path(backend, req))
	if err != nil {
		return Result{}, time.Time{}, false
	}
	var e cacheEntry
	if json.Unmarshal(data, &e) != nil {
		return Result{}, time.Time{}, false
	}
	return e.Result, e.Time, true
}

// Put stores a result.
func (c *Cache) Put(backend string, req Request, r Result) error {
	if c == nil || c.Dir == "" {
		return nil
	}
	if err := os.MkdirAll(c.Dir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(cacheEntry{Time: time.Now(), Result: r})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(c.Dir, "*.tmp")
	if err != nil {
		return err
	}
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), c.path(backend, req))
}
