// Package catalog owns the set of tools available at runtime.
package catalog

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"opentools/pkg/tool"
)

// Registry is safe for concurrent readers and atomic replacement when
// installed plugins change.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]tool.Tool
	defs  []tool.Definition
}

// New returns an empty registry.
func New() *Registry {
	return &Registry{tools: map[string]tool.Tool{}}
}

// Add registers a tool and indexes its definition.
func (r *Registry) Add(t tool.Tool) error {
	if t == nil {
		return fmt.Errorf("register tool: nil tool")
	}
	def := t.Definition()
	if err := tool.ValidateDefinition(def); err != nil {
		return err
	}
	def.ID = strings.TrimSpace(def.ID)
	def.Category = strings.TrimSpace(def.Category)

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[def.ID]; exists {
		return fmt.Errorf("register tool %q: id is already registered", def.ID)
	}
	r.tools[def.ID] = t
	r.defs = append(r.defs, def)
	sort.SliceStable(r.defs, func(i, j int) bool {
		if r.defs[i].Category != r.defs[j].Category {
			return r.defs[i].Category < r.defs[j].Category
		}
		return r.defs[i].Name < r.defs[j].Name
	})
	return nil
}

// Replace atomically swaps the complete tool set. A failed validation leaves
// the current registry untouched, so an installed tool never disappears
// halfway through a plugin-store refresh.
func (r *Registry) Replace(tools []tool.Tool) error {
	next := make(map[string]tool.Tool, len(tools))
	defs := make([]tool.Definition, 0, len(tools))
	for _, candidate := range tools {
		if candidate == nil {
			return fmt.Errorf("register tool: nil tool")
		}
		def := candidate.Definition()
		if err := tool.ValidateDefinition(def); err != nil {
			return err
		}
		def.ID = strings.TrimSpace(def.ID)
		def.Category = strings.TrimSpace(def.Category)
		if _, exists := next[def.ID]; exists {
			return fmt.Errorf("register tool %q: id is already registered", def.ID)
		}
		next[def.ID] = candidate
		defs = append(defs, def)
	}
	sort.SliceStable(defs, func(i, j int) bool {
		if defs[i].Category != defs[j].Category {
			return defs[i].Category < defs[j].Category
		}
		return defs[i].Name < defs[j].Name
	})
	r.mu.Lock()
	r.tools = next
	r.defs = defs
	r.mu.Unlock()
	return nil
}

// Definitions returns a detached list of tool definitions.
func (r *Registry) Definitions() []tool.Definition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]tool.Definition(nil), r.defs...)
}

// Get returns the tool registered under id.
func (r *Registry) Get(id string) (tool.Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[id]
	return t, ok
}
