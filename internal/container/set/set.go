package set

type Set[T comparable] map[T]struct{}

func (s Set[T]) Add(key T) {
	s[key] = struct{}{}
}

func (s Set[T]) Contains(key T) bool {
	_, ok := s[key]
	return ok
}
