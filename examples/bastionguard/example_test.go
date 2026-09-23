package bastionguard_test

import (
	"time"

	"github.com/JonasBorgesLM/bastion"
	"github.com/redis/go-redis/v9"

	"github.com/JonasBorgesLM/cistern/examples/bastionguard"
	"github.com/JonasBorgesLM/cistern/redisstore"
)

func ExampleGuard() {
	breaker, err := bastion.New("redis-l2", bastion.WithFailureThreshold(5), bastion.WithOpenTimeout(10*time.Second))
	if err != nil {
		panic(err)
	}
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379", ContextTimeoutEnabled: true})
	defer client.Close()

	l2, err := redisstore.New(client,
		redisstore.WithTimeout(50*time.Millisecond),
		redisstore.WithGuard(bastionguard.Guard{Breaker: breaker}))
	if err != nil {
		panic(err)
	}
	_ = l2 // cistern.WithL2(l2)
}
