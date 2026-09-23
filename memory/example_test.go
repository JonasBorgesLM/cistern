package memory_test

import (
	"context"
	"fmt"
	"time"

	"github.com/JonasBorgesLM/cistern/memory"
)

func ExampleNew() {
	l1, err := memory.New(memory.WithMaxEntries(1000), memory.WithMaxBytes(1<<20))
	if err != nil {
		panic(err)
	}
	ctx := context.Background()

	if err := l1.Set(ctx, "cistern:v1:tasks:-:k:user:42:list", []byte(`[1,2,3]`), time.Minute); err != nil {
		panic(err)
	}
	value, ok, err := l1.Get(ctx, "cistern:v1:tasks:-:k:user:42:list")
	if err != nil {
		panic(err)
	}
	fmt.Println(string(value), ok)
	// Output: [1,2,3] true
}
