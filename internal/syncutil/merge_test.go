package syncutil

import (
	"context"
	"testing"

	"golang.org/x/sync/errgroup"
)

func TestMerge(t *testing.T) {
	var merge Merge[int]

	// generate expected
	size := 100
	factor := -1
	var expected int
	for i := 1; i < size; i++ {
		expected += i * factor
	}

	// test merge
	ctx := context.Background()
	eg, _ := errgroup.WithContext(ctx)
	var result int
	for i := 1; i < size; i++ {
		eg.Go(func(num int) func() error {
			return func() error {
				var f int
				getFactor := func() error {
					f = factor
					return nil
				}
				calculate := func(items []int) error {
					for _, item := range items {
						result += item * f
					}
					return nil
				}
				return merge.Do(num, getFactor, calculate)
			}
		}(i))
	}
	if err := eg.Wait(); err != nil {
		t.Errorf("Merge.Do() error = %v, wantErr %v", err, nil)
	}
	if result != expected {
		t.Errorf("Merge.Do() = %v, want %v", result, expected)
	}
}
