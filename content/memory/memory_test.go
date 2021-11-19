package memory

import (
	"testing"

	"oras.land/oras-go"
)

func TestStoreInterface(t *testing.T) {
	var store interface{} = &Store{}
	if _, ok := store.(oras.Target); !ok {
		t.Error("&Store{} does not conform oras.Target")
	}
}
