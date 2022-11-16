package syncutil

import (
	"fmt"
	"testing"
)

func TestPool(t *testing.T) {
	var pool Pool[int]
	i, done := pool.Get("foo")
	*i++

	i, done = pool.Get("foo")
	*i++
	done()

	i, done = pool.Get("foo")
	*i++
	done()

	i, done = pool.Get("foo")
	fmt.Println(*i)
	fmt.Println(pool.items["foo"].refCount)
	done()
	done()

	fmt.Println(pool.items["foo"])
}
