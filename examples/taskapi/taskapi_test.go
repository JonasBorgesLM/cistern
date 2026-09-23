package taskapi_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/JonasBorgesLM/cistern"
	"github.com/JonasBorgesLM/cistern/examples/taskapi"
	"github.com/JonasBorgesLM/cistern/memory"
)

// source is the Repository behind the cache: the source of truth, counting
// how often it is asked.
type source struct {
	mu    sync.Mutex
	tasks []taskapi.Task
	lists map[int]int // ListByOwner calls per owner
}

func newSource(tasks ...taskapi.Task) *source {
	return &source{tasks: tasks, lists: map[int]int{}}
}

func (s *source) ListByOwner(_ context.Context, owner int) ([]taskapi.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lists[owner]++
	var out []taskapi.Task
	for _, t := range s.tasks {
		if t.Owner == owner {
			out = append(out, t)
		}
	}
	return out, nil
}

func (s *source) Create(_ context.Context, t taskapi.Task) (taskapi.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t.ID = len(s.tasks) + 1
	s.tasks = append(s.tasks, t)
	return t, nil
}

func (s *source) calls(owner int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lists[owner]
}

func newRepo(t *testing.T, src taskapi.Repository) *taskapi.CachedRepository {
	t.Helper()
	l1, err := memory.New()
	if err != nil {
		t.Fatal(err)
	}
	r, err := taskapi.NewCachedRepository(src, cistern.WithL1(l1), cistern.WithTTL(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func titles(ts []taskapi.Task) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.Title)
	}
	return out
}

func TestListsAreCachedPerOwner(t *testing.T) {
	ctx := context.Background()
	src := newSource(taskapi.Task{ID: 1, Owner: 42, Title: "a"})
	repo := newRepo(t, src)
	for range 3 {
		if _, err := repo.ListByOwner(ctx, 42); err != nil {
			t.Fatal(err)
		}
	}
	if got := src.calls(42); got != 1 {
		t.Fatalf("source asked %d times, want 1", got)
	}
}

// RS-02, T-01, sapper scenario 6 in miniature: a user never receives another
// user's cached list.
//
// The owner is in both the key and the tag, and either one alone keeps users
// apart: with the owner missing from the key only, the entry records its
// owner's tag generation, which never matches another owner's (ADR-0005).
// Negative control: verified failing with the owner left out of both;
// verified still passing with it left out of the key alone.
func TestOneUserNeverSeesAnothersCachedList(t *testing.T) {
	ctx := context.Background()
	src := newSource(
		taskapi.Task{ID: 1, Owner: 42, Title: "private to 42"},
		taskapi.Task{ID: 2, Owner: 43, Title: "private to 43"},
	)
	repo := newRepo(t, src)
	if _, err := repo.ListByOwner(ctx, 42); err != nil { // 42's list is now cached
		t.Fatal(err)
	}
	got, err := repo.ListByOwner(ctx, 43)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(titles(got), []string{"private to 43"}) {
		t.Fatalf("user 43 received %v", titles(got))
	}
}

// ADR-0005, ADR-0011: a write invalidates its owner's lists by tag, so the
// next read sees it — and only that owner's lists are affected.
// Negative control: verified failing with Create not invalidating.
func TestCreateInvalidatesOnlyItsOwnersLists(t *testing.T) {
	ctx := context.Background()
	src := newSource(taskapi.Task{ID: 1, Owner: 42, Title: "a"}, taskapi.Task{ID: 2, Owner: 43, Title: "b"})
	repo := newRepo(t, src)
	for _, owner := range []int{42, 43} {
		if _, err := repo.ListByOwner(ctx, owner); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repo.Create(ctx, taskapi.Task{Owner: 42, Title: "new"}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.ListByOwner(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(titles(got), []string{"a", "new"}) {
		t.Fatalf("after Create, user 42's list = %v", titles(got))
	}
	if _, err := repo.ListByOwner(ctx, 43); err != nil {
		t.Fatal(err)
	}
	if src.calls(43) != 1 {
		t.Fatalf("user 43's list was reloaded %d times; a write by 42 must not touch it", src.calls(43))
	}
}

// ADR-0002: the task was created; the invalidation failure is reported, not
// turned into a failed Create that a caller would retry into a duplicate.
func TestInvalidationFailureIsReportedNotReturned(t *testing.T) {
	ctx := context.Background()
	failing := failingBumpStore(t)
	var reported error
	repo, err := taskapi.NewCachedRepository(newSource(), cistern.WithL1(failing), cistern.WithTTL(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	repo.OnInvalidationError = func(err error) { reported = err }
	task, err := repo.Create(ctx, taskapi.Task{Owner: 42, Title: "new"})
	if err != nil || task.ID == 0 {
		t.Fatalf("Create = %+v, %v; want the created task", task, err)
	}
	if !errors.Is(reported, errBump) {
		t.Fatalf("reported = %v, want the invalidation error", reported)
	}
}

var errBump = errors.New("counter store down")

type bumpFails struct{ *memory.Store }

func (bumpFails) Bump(context.Context, []string, time.Duration) error { return errBump }

func failingBumpStore(t *testing.T) cistern.Store {
	t.Helper()
	m, err := memory.New()
	if err != nil {
		t.Fatal(err)
	}
	return bumpFails{m}
}
