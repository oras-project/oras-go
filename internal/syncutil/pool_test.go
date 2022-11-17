package syncutil

import (
	"sync"
	"testing"
)

func TestPool(t *testing.T) {
	var pool Pool[int]
	numbers := [][]int{
		{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
		{-1, -2, -3, -4, -5, -6, -7, -8, -9, -10},
	}

	// generate expected result
	expected := make([]int, len(numbers))
	for i, nums := range numbers {
		for _, num := range nums {
			expected[i] += num
		}
	}

	// test pool
	for i, nums := range numbers {
		val, done := pool.Get(i)
		*val = 0
		var wg sync.WaitGroup
		for _, num := range nums {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()
				val, done := pool.Get(i)
				defer done()
				*val += n
			}(num)
		}
		wg.Wait()
		item := pool.items[i]
		if got := item.value; got != expected[i] {
			t.Errorf("Pool.Get(%v).value = %v, want %v", i, got, expected[i])
		}
		if got := item.refCount; got != 1 {
			t.Errorf("Pool.Get(%v).refCount = %v, want %v", i, got, 1)
		}

		// item should be cleaned up after done
		done()
		got := pool.items[i]
		if got != nil {
			t.Errorf("Pool.Get(%v) = %v, want %v", i, got, nil)
		}
	}
}
