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

func TestFixedListsSystemDriveFirst(t *testing.T) {
	drives, err := Fixed()
	if err != nil {
		t.Fatal(err)
	}
	if len(drives) == 0 || !drives[0].System || drives[0].Total == 0 {
		t.Fatalf("%+v", drives)
	}
	if !IsFixed(drives[0].Drive) {
		t.Fatalf("%s debía contar como fijo", drives[0].Drive)
	}
	if IsFixed("no es un disco") || IsFixed("") {
		t.Fatal("aceptó algo que no es un disco")
	}
}
