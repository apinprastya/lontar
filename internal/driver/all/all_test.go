package all

import (
	"testing"

	"lontar/internal/driver"
)

func TestRegistryHasAllKinds(t *testing.T) {
	r := Registry()
	list := r.List()
	if len(list) != len(driver.Kinds) {
		t.Fatalf("List() = %+v", list)
	}
	for i, k := range driver.Kinds {
		if list[i].Kind != k {
			t.Errorf("List()[%d] = %q, want %q", i, list[i].Kind, k)
		}
		if _, err := r.Get(k); err != nil {
			t.Error(err)
		}
	}
}
