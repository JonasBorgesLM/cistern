// Package taskapi shows cistern as a Repository Decorator, the shape task-api
// uses (REQUIREMENTS.md §10): the Service keeps calling a Repository and is
// handed a cached one; nothing above the Repository changes.
//
// Every list is keyed and tagged by its owner, so one user can never be
// served another's list (RS-02), and every write invalidates its owner's
// lists by tag, which closes the cache-aside race for them (ADR-0011).
package taskapi

import (
	"context"
	"fmt"

	"github.com/JonasBorgesLM/cistern"
)

// Task is the entity being cached.
type Task struct {
	ID    int    `json:"id"`
	Owner int    `json:"owner"`
	Title string `json:"title"`
}

// Repository is the persistence port the Service depends on.
type Repository interface {
	ListByOwner(ctx context.Context, owner int) ([]Task, error)
	Create(ctx context.Context, t Task) (Task, error)
}

// CachedRepository decorates a Repository with a cache of each owner's list.
type CachedRepository struct {
	next  Repository
	lists *cistern.Cache[int, []Task]

	// OnInvalidationError receives an invalidation that failed after a write
	// succeeded. The write is not undone and not reported as failed — a
	// caller would retry it into a duplicate — but the owner's lists may be
	// stale until their TTL, so it is worth logging (ADR-0002). Nil ignores it.
	OnInvalidationError func(error)
}

// listsTag is the tag every list of owner carries, and the key it is cached
// under: the owner is part of both, so no key can reach another user's data.
func listsTag(owner int) string { return fmt.Sprintf("user:%d:lists", owner) }

// NewCachedRepository wraps next. opts configure the cache's levels and TTLs;
// the namespace, key and tags are the decorator's.
func NewCachedRepository(next Repository, opts ...cistern.Option) (*CachedRepository, error) {
	opts = append(opts, cistern.WithTags(func(owner int) []string { return []string{listsTag(owner)} }))
	lists, err := cistern.New[int, []Task]("tasks", listsTag, opts...)
	if err != nil {
		return nil, err
	}
	return &CachedRepository{next: next, lists: lists}, nil
}

// ListByOwner returns owner's tasks, from the cache when it can.
func (r *CachedRepository) ListByOwner(ctx context.Context, owner int) ([]Task, error) {
	return r.lists.GetOrLoad(ctx, owner, func(ctx context.Context) ([]Task, error) {
		return r.next.ListByOwner(ctx, owner)
	})
}

// Create writes through to the Repository, then invalidates the owner's
// lists.
func (r *CachedRepository) Create(ctx context.Context, t Task) (Task, error) {
	created, err := r.next.Create(ctx, t)
	if err != nil {
		return Task{}, err
	}
	if err := r.lists.InvalidateTag(ctx, listsTag(created.Owner)); err != nil && r.OnInvalidationError != nil {
		r.OnInvalidationError(err)
	}
	return created, nil
}
