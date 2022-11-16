package syncutil

import (
	"context"
	"testing"

	"golang.org/x/sync/errgroup"
)

func TestMerge(t *testing.T) {
	var merge Merge[int]
	var learnt bool
	nums := []int{1, -2, 4, 5, 1, 7, 10, -6, 3, 0}
	var expected int

	for _, n := range nums {
		expected += n
	}

	ctx := context.Background()
	eg, _ := errgroup.WithContext(ctx)
	var result int
	for _, n := range nums {
		eg.Go(func(num int) func() error {
			return func() error {
				return merge.Do(num, func() error {
					learnt = true
					return nil
				}, func(items []int) error {
					for _, i := range items {
						result += i
					}
					return nil
				})
			}
		}(n))
	}
	if err := eg.Wait(); err != nil {
		t.Error(err)
	}
	if !learnt {
		t.Errorf("learnt: %v, want %v", learnt, true)
	}
	if result != expected {
		t.Errorf("result = %v, expected %v", result, expected)
	}
}
