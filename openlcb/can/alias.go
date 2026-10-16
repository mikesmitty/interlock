package can

import (
	"sync"

	"github.com/mikesmitty/interlock/openlcb"
)

// AliasGenerator produces the pseudo-random alias sequence for a node ID
// from the CAN Frame Transfer technical note, so a node tries the same
// aliases in the same order every time it starts.
type AliasGenerator struct {
	lfsr1, lfsr2 uint32
}

// NewAliasGenerator seeds the sequence from id.
func NewAliasGenerator(id openlcb.NodeID) *AliasGenerator {
	return &AliasGenerator{
		lfsr1: uint32(uint64(id)>>24) & 0xFFFFFF,
		lfsr2: uint32(uint64(id)) & 0xFFFFFF,
	}
}

// Next returns the next candidate alias, never zero.
func (g *AliasGenerator) Next() openlcb.Alias {
	for {
		t1 := (g.lfsr1<<9 | (g.lfsr2>>15)&0x1FF) & 0xFFFFFF
		t2 := (g.lfsr2 << 9) & 0xFFFFFF
		g.lfsr1 = g.lfsr1 + t1 + 0x1B0CA3
		g.lfsr2 = g.lfsr2 + t2 + 0x7A4BA9
		g.lfsr1 = (g.lfsr1 & 0xFFFFFF) + (g.lfsr2&0xFF000000)>>24
		g.lfsr2 &= 0xFFFFFF
		alias := openlcb.Alias((g.lfsr1 ^ g.lfsr2 ^ (g.lfsr1 >> 12) ^ (g.lfsr2 >> 12)) & 0xFFF)
		if alias != 0 {
			return alias
		}
	}
}

// AliasCache maps the aliases seen on the bus to node IDs, learned from
// AMD frames and from messages that carry the sender's ID. It is safe for
// concurrent use.
type AliasCache struct {
	mu      sync.RWMutex
	byAlias map[openlcb.Alias]openlcb.NodeID
	byID    map[openlcb.NodeID]openlcb.Alias
}

// NewAliasCache returns an empty cache.
func NewAliasCache() *AliasCache {
	return &AliasCache{
		byAlias: make(map[openlcb.Alias]openlcb.NodeID),
		byID:    make(map[openlcb.NodeID]openlcb.Alias),
	}
}

// Add records that alias belongs to id, replacing any earlier mapping of
// either.
func (c *AliasCache) Add(alias openlcb.Alias, id openlcb.NodeID) {
	if alias == 0 || id == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if old, ok := c.byAlias[alias]; ok && old != id {
		delete(c.byID, old)
	}
	if old, ok := c.byID[id]; ok && old != alias {
		delete(c.byAlias, old)
	}
	c.byAlias[alias] = id
	c.byID[id] = alias
}

// Remove forgets an alias.
func (c *AliasCache) Remove(alias openlcb.Alias) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id, ok := c.byAlias[alias]; ok {
		delete(c.byID, id)
		delete(c.byAlias, alias)
	}
}

// Lookup returns the node ID using alias.
func (c *AliasCache) Lookup(alias openlcb.Alias) (openlcb.NodeID, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	id, ok := c.byAlias[alias]
	return id, ok
}

// LookupID returns the alias a node ID is using.
func (c *AliasCache) LookupID(id openlcb.NodeID) (openlcb.Alias, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	alias, ok := c.byID[id]
	return alias, ok
}

// Handle fills in whichever half of a handle the cache knows.
func (c *AliasCache) Handle(alias openlcb.Alias) openlcb.NodeHandle {
	id, _ := c.Lookup(alias)
	return openlcb.NodeHandle{ID: id, Alias: alias}
}

// Len returns the number of mappings.
func (c *AliasCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.byAlias)
}

// Entries returns a copy of every mapping.
func (c *AliasCache) Entries() map[openlcb.Alias]openlcb.NodeID {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[openlcb.Alias]openlcb.NodeID, len(c.byAlias))
	for alias, id := range c.byAlias {
		out[alias] = id
	}
	return out
}
