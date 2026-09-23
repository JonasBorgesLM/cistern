package codec_test

import (
	"fmt"

	"github.com/JonasBorgesLM/cistern/codec"
)

func ExampleJSON() {
	type task struct {
		ID    int    `json:"id"`
		Title string `json:"title"`
	}

	data, err := codec.JSON.Marshal(task{ID: 1, Title: "measure before caching"})
	if err != nil {
		panic(err)
	}
	fmt.Println(string(data))

	var t task
	if err := codec.JSON.Unmarshal(data, &t); err != nil {
		panic(err)
	}
	fmt.Println(t.Title)
	// Output:
	// {"id":1,"title":"measure before caching"}
	// measure before caching
}
