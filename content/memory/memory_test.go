package memory

import (
	"testing"

	"oras.land/oras-go"
	"oras.land/oras-go/content"
)

func TestStoreInterface(t *testing.T) {
	var store interface{} = &Store{}
	if _, ok := store.(oras.Target); !ok {
		t.Error("&Store{} does not conform oras.Target")
	}
	if _, ok := store.(content.UpEdgeFinder); !ok {
		t.Error("&Store{} does not conform content.UpEdgeFinder")
	}
}
