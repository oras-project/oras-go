package syncutil

import "context"

type Once struct {
	result interface{}
	err    error
	status chan bool
}

func NewOnce() *Once {
	status := make(chan bool, 1)
	status <- true
	return &Once{
		status: status,
	}
}

func (o *Once) Do(ctx context.Context, f func() (interface{}, error)) (bool, interface{}, error) {
	for {
		select {
		case inProgress := <-o.status:
			if !inProgress {
				return false, o.result, o.err
			}
			result, err := f()
			if err == context.Canceled || err == context.DeadlineExceeded {
				o.status <- true
				return false, "", err
			}
			o.result, o.err = result, err
			close(o.status)
			return true, result, err
		case <-ctx.Done():
			return false, "", ctx.Err()
		}
	}
}
