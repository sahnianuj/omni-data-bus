package connectors

import (
	"fmt"
	"net/http"
	"sort"
	"sync"

	"omnidatabus/internal/problem"
)

// Registry maps a connector type (e.g. "postgres") to its implementation of T.
type Registry[T interface{ Descriptor() Descriptor }] struct {
	mu   sync.RWMutex
	role Role
	m    map[string]T
}

// NewRegistry returns an empty registry for role.
func NewRegistry[T interface{ Descriptor() Descriptor }](role Role) *Registry[T] {
	return &Registry[T]{role: role, m: map[string]T{}}
}

// Register adds a connector. Registering the same type twice is a programming error.
func (r *Registry[T]) Register(c T) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t := c.Descriptor().Type
	if _, dup := r.m[t]; dup {
		panic(fmt.Sprintf("connectors: %s connector %q registered twice", r.role, t))
	}
	r.m[t] = c
}

// UnsupportedTypeError is returned for an unknown connector type.
type UnsupportedTypeError struct {
	Role Role
	Type string
}

func (e *UnsupportedTypeError) Error() string {
	return fmt.Sprintf("unsupported %s type %q", e.Role, e.Type)
}

// Problem implements problem.Problemer.
func (e *UnsupportedTypeError) Problem() problem.Problem {
	code := problem.CodeUnsupportedSource
	if e.Role == RoleTarget {
		code = problem.CodeUnsupportedTarget
	}
	p := problem.New(http.StatusUnprocessableEntity, code, "Unsupported database type", fmt.Sprintf("%q isn't a supported %s type yet.", e.Type, e.Role))
	p.FieldErrors = []problem.FieldError{{Field: "type", Message: "is not supported"}}
	return p
}

// Get returns the connector for type t.
func (r *Registry[T]) Get(t string) (T, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.m[t]
	if !ok {
		var zero T
		return zero, &UnsupportedTypeError{Role: r.role, Type: t}
	}
	return c, nil
}

// Descriptors lists registered connectors sorted by display name.
func (r *Registry[T]) Descriptors() []Descriptor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Descriptor, 0, len(r.m))
	for _, c := range r.m {
		out = append(out, c.Descriptor())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DisplayName < out[j].DisplayName })
	return out
}
