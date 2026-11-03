package disk

import "testing"

func TestSystem(t *testing.T) {
	usage, err := System()
	if err != nil {
		t.Fatal(err)
	}
	if usage.Drive == "" {
		t.Error("Drive vacío")
	}
	if usage.Total == 0 {
		t.Error("Total en cero")
	}
	if usage.Free > usage.Total {
		t.Errorf("Free %d mayor que Total %d", usage.Free, usage.Total)
	}
}
