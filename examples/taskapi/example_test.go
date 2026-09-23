package taskapi_test

import (
	"context"
	"fmt"
	"time"

	"github.com/JonasBorgesLM/cistern"
	"github.com/JonasBorgesLM/cistern/examples/taskapi"
	"github.com/JonasBorgesLM/cistern/memory"
)

// The Service layer keeps calling a Repository; it is handed the cached one.
func ExampleNewCachedRepository() {
	src := newSource(taskapi.Task{ID: 1, Owner: 42, Title: "measure before caching"})
	l1, _ := memory.New()
	repo, err := taskapi.NewCachedRepository(src, cistern.WithL1(l1), cistern.WithTTL(time.Minute))
	if err != nil {
		panic(err)
	}
	ctx := context.Background()

	list, _ := repo.ListByOwner(ctx, 42)
	fmt.Println(titles(list))

	_, _ = repo.Create(ctx, taskapi.Task{Owner: 42, Title: "write ADR-0005"})
	list, _ = repo.ListByOwner(ctx, 42)
	fmt.Println(titles(list))
	// Output:
	// [measure before caching]
	// [measure before caching write ADR-0005]
}
