package actions

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

const hydratedAttrs = windows.FILE_ATTRIBUTE_REPARSE_POINT | windows.FILE_ATTRIBUTE_ARCHIVE

// fakeCloud describe el estado de nube de rutas reales en una carpeta temporal.
type fakeCloud struct {
	files   map[string]cloudFile
	writes  map[string]uint32
	pending map[string]bool
}

func (f *fakeCloud) fs() freeUpFS {
	return freeUpFS{
		read: func(path string) (cloudFile, error) {
			if file, ok := f.files[path]; ok {
				return file, nil
			}
			return readCloudFile(path)
		},
		inSync: func(path string) bool { return !f.pending[path] },
		write: func(path string, attrs uint32) error {
			f.writes[path] = attrs
			return nil
		},
	}
}

func cloudFixture(t *testing.T) (string, *fakeCloud, map[string]string) {
	t.Helper()
	root := tempDir(t)
	paths := map[string]string{
		"subido":   filepath.Join(root, "subido.bin"),
		"fijado":   filepath.Join(root, "sub", "fijado.bin"),
		"enNube":   filepath.Join(root, "en-nube.bin"),
		"onedrive": filepath.Join(root, "onedrive.bin"),
		"local":    filepath.Join(root, "local.bin"),
		"otraTag":  filepath.Join(root, "otra-etiqueta.bin"),
	}
	for _, path := range paths {
		writeFile(t, path, "12345")
	}
	cloud := &fakeCloud{writes: map[string]uint32{}, files: map[string]cloudFile{
		paths["subido"]:   {attrs: hydratedAttrs, tag: reparseTagCloud},
		paths["fijado"]:   {attrs: hydratedAttrs | attrPinned, tag: reparseTagCloud | 0x1000},
		paths["enNube"]:   {attrs: hydratedAttrs | windows.FILE_ATTRIBUTE_RECALL_ON_DATA_ACCESS, tag: reparseTagCloud},
		paths["onedrive"]: {attrs: hydratedAttrs, tag: reparseTagOneDrive},
		paths["otraTag"]:  {attrs: hydratedAttrs, tag: 0x8000001B},
	}}
	return root, cloud, paths
}

func TestFreeUpOnlyTouchesCloudFiles(t *testing.T) {
	root, cloud, paths := cloudFixture(t)
	result := freeUp(context.Background(), root, cloud.fs())

	if result.Files != 3 || result.Requested != 15 || result.Already != 1 || result.Local != 2 || result.Failed != 0 {
		t.Fatalf("%+v", result)
	}
	for _, name := range []string{"enNube", "local", "otraTag"} {
		if _, wrote := cloud.writes[paths[name]]; wrote {
			t.Errorf("%s no debía tocarse", name)
		}
	}
	got := cloud.writes[paths["fijado"]]
	if got&attrUnpinned == 0 || got&attrPinned != 0 {
		t.Fatalf("debe quitar el fijado y pedir liberar: %#x", got)
	}
	// Conserva el resto de los atributos.
	if got&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0 || got&windows.FILE_ATTRIBUTE_ARCHIVE == 0 {
		t.Fatalf("perdió atributos: %#x", got)
	}
	// Los archivos reales siguen idénticos: nada los abrió ni cambió.
	for name, path := range paths {
		if data, err := os.ReadFile(path); err != nil || string(data) != "12345" {
			t.Errorf("%s: %q, %v", name, data, err)
		}
	}
}

// Lo que no ha terminado de subir no se pide: sería la única copia.
func TestFreeUpSkipsWhatHasNotUploaded(t *testing.T) {
	root, cloud, paths := cloudFixture(t)
	cloud.pending = map[string]bool{paths["subido"]: true}
	result := freeUp(context.Background(), root, cloud.fs())
	if result.Pending != 1 || result.Files != 2 || result.Requested != 10 {
		t.Fatalf("%+v", result)
	}
	if _, wrote := cloud.writes[paths["subido"]]; wrote {
		t.Fatal("marcó un archivo que aún no sube")
	}
}

// Sin datos de sincronización, la respuesta segura es que no ha subido.
func TestPlaceholderInSyncFailsClosed(t *testing.T) {
	root := tempDir(t)
	path := filepath.Join(root, "normal.bin")
	writeFile(t, path, "x")
	if placeholderInSync(path) {
		t.Fatal("un archivo sin nube no puede contar como subido")
	}
	if placeholderInSync(filepath.Join(root, "no-existe")) {
		t.Fatal("un archivo inexistente no puede contar como subido")
	}
}

func TestFreeUpAlreadyUnpinnedDoesNotWrite(t *testing.T) {
	root := tempDir(t)
	path := filepath.Join(root, "a.bin")
	writeFile(t, path, "x")
	cloud := &fakeCloud{writes: map[string]uint32{}, files: map[string]cloudFile{
		path: {attrs: hydratedAttrs | attrUnpinned, tag: reparseTagCloud},
	}}
	result := freeUp(context.Background(), root, cloud.fs())
	if result.Files != 1 || len(cloud.writes) != 0 {
		t.Fatalf("%+v, %v", result, cloud.writes)
	}
}

func TestFreeUpOnNormalFolderChangesNothing(t *testing.T) {
	root := tempDir(t)
	path := filepath.Join(root, "normal.bin")
	writeFile(t, path, "x")
	before, err := readCloudFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// El código real, con escritura real: sin marca de nube no debe escribir nada.
	result := FreeUpSpace(context.Background(), root)
	if result.Files != 0 || result.Local != 1 || result.Failed != 0 {
		t.Fatalf("%+v", result)
	}
	after, err := readCloudFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if before.attrs != after.attrs {
		t.Fatalf("cambió atributos: %#x a %#x", before.attrs, after.attrs)
	}
}

func TestFreeUpDoesNotFollowJunctions(t *testing.T) {
	root := tempDir(t)
	outside := filepath.Join(root, "afuera")
	inside := filepath.Join(root, "dentro")
	target := filepath.Join(outside, "ajeno.bin")
	writeFile(t, target, "x")
	writeFile(t, filepath.Join(inside, "propio.bin"), "x")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(inside, "enlace"), outside).CombinedOutput(); err != nil {
		t.Fatalf("mklink: %v: %s", err, out)
	}
	cloud := &fakeCloud{writes: map[string]uint32{}, files: map[string]cloudFile{
		target: {attrs: hydratedAttrs, tag: reparseTagCloud},
		filepath.Join(inside, "enlace", "ajeno.bin"): {attrs: hydratedAttrs, tag: reparseTagCloud},
	}}
	result := freeUp(context.Background(), inside, cloud.fs())
	if len(cloud.writes) != 0 || result.Files != 0 {
		t.Fatalf("siguió el enlace: %+v, %v", result, cloud.writes)
	}
}

func TestFreeUpSurveyWritesNothing(t *testing.T) {
	root, cloud, _ := cloudFixture(t)
	disk := cloud.fs()
	disk.write = nil
	result := freeUp(context.Background(), root, disk)
	if result.Files != 3 || len(cloud.writes) != 0 {
		t.Fatalf("%+v, %v", result, cloud.writes)
	}
}

func TestFreeUpCancel(t *testing.T) {
	root, cloud, _ := cloudFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := freeUp(ctx, root, cloud.fs())
	if !result.Canceled || len(cloud.writes) != 0 {
		t.Fatalf("%+v", result)
	}
}

func TestCheckFreeUp(t *testing.T) {
	root := tempDir(t)
	folder := filepath.Join(root, "nube")
	writeFile(t, filepath.Join(folder, "a.bin"), "x")
	synced := func(string) bool { return true }

	if err := CheckFreeUp(folder, synced); err != nil {
		t.Fatal(err)
	}
	wantCode(t, CheckFreeUp(folder, func(string) bool { return false }), CodeNotSynced)
	wantCode(t, CheckFreeUp(`\\servidor\nube`, synced), CodeInvalidPath)
	wantCode(t, CheckFreeUp(filepath.Join(root, "no-existe"), synced), CodeMissing)

	junction := filepath.Join(root, "enlace")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", junction, folder).CombinedOutput(); err != nil {
		t.Fatalf("mklink: %v: %s", err, out)
	}
	wantCode(t, CheckFreeUp(junction, synced), CodeLink)
	wantCode(t, CheckFreeUp(filepath.Join(junction, "a.bin"), synced), CodeLink)
}

// Revisión real, solo lectura. Se activa con EL_BUSCADOR_REAL_FREEUP='C:\ruta'.
func TestRealFreeUpSurvey(t *testing.T) {
	path := os.Getenv("EL_BUSCADOR_REAL_FREEUP")
	if path == "" {
		t.Skip("define EL_BUSCADOR_REAL_FREEUP")
	}
	result := SurveyFreeUp(context.Background(), path)
	t.Logf("%s: pediría %d archivos (%d MB); sin subir %d; ya en la nube %d; sin marca de nube %d; fallaron %d",
		path, result.Files, result.Requested/1024/1024, result.Pending, result.Already, result.Local, result.Failed)
}
