package crierhooks_test

import (
	"time"

	"github.com/JonasBorgesLM/crier/core"

	"github.com/JonasBorgesLM/cistern"
	"github.com/JonasBorgesLM/cistern/examples/crierhooks"
	"github.com/JonasBorgesLM/cistern/memory"
)

func ExampleHooks() {
	logs, err := core.New(core.Options{ServiceName: "task-api", Exporters: map[string]core.Exporter{"stdout": &collector{}}})
	if err != nil {
		panic(err)
	}
	l1, _ := memory.New()
	_, _ = cistern.New[string, string]("tasks", func(k string) string { return k },
		cistern.WithL1(l1), cistern.WithTTL(time.Minute),
		cistern.WithHooks(crierhooks.Hooks(logs)))
}
