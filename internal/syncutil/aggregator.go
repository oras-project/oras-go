package syncutil

import "context"

type Aggregator struct {
	result interface{}
	err    error
	status chan bool
}

func NewAggregator() *Aggregator {
	status := make(chan bool, 1)
	status <- true
	return &Aggregator{
		status: status,
	}
}

func (a *Aggregator) Do(ctx context.Context, f func() (interface{}, error)) (bool, interface{}, error) {
	for {
		select {
		case inProgress := <-a.status:
			if !inProgress {
				return false, a.result, a.err
			}
			result, err := f()
			if err == context.Canceled || err == context.DeadlineExceeded {
				a.status <- true
				return false, "", err
			}
			a.result, a.err = result, err
			close(a.status)
			return true, result, err
		case <-ctx.Done():
			return false, "", ctx.Err()
		}
	}
}
