package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Item is a credential entry. It only ever holds metadata needed to fetch a
// password on demand — never the password itself.
type Item struct {
	Title       string `json:"title"`
	Group       string `json:"group"`
	Path        string `json:"path"`
	HasOTP      bool   `json:"has_otp,omitempty"`
	HasPassword bool   `json:"has_password,omitempty"`
}

type cache struct {
	Updated time.Time `json:"updated"`
	Items   []Item    `json:"items"`
}

func loadCache(path string) (*cache, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c cache
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func saveCache(path string, items []Item) error {
	c := cache{
		Updated: time.Now(),
		Items:   items,
	}
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (c *cache) stale(ttl time.Duration) bool {
	return c == nil || time.Since(c.Updated) > ttl
}
