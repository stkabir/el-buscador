package duplicates

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"

	"golang.org/x/sys/windows"

	"el-buscador/internal/actions"
)

const testSize = 1024 * 1024

func content(fill byte) []byte {
	data := make([]byte, testSize)
	for i := range data {
		data[i] = fill
	}
	return data
}

func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// put escribe un archivo y devuelve su descripción, como la daría el escaneo.
func put(t *testing.T, path string, data []byte) File {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return describe(t, path)
}

func describe(t *testing.T, path string) File {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return File{Path: path, Size: info.Size(), Allocated: info.Size(), Modified: info.ModTime().UnixNano()}
}

func groupPaths(result Result) [][]string {
	paths := [][]string{}
	for _, group := range result.Groups {
		var names []string
		for _, file := range group.Files {
			names = append(names, filepath.Base(file.Path))
		}
		paths = append(paths, names)
	}
	return paths
}

func find(files ...File) Result {
	return Find(context.Background(), files, DiskReader{}, nil, nil)
}

// El fixture obligatorio de la fase: mismo tamaño no significa mismo contenido.
func TestSameSizeDifferentContentDoesNotMatch(t *testing.T) {
	dir := tempDir(t)
	original := content('a')

	middle := slices.Clone(original)
	middle[testSize/2] = 'x' // el hash parcial no ve este byte
	last := slices.Clone(original)
	last[testSize-1] = 'x'
	first := slices.Clone(original)
	first[0] = 'x'

	result := find(
		put(t, filepath.Join(dir, "a.bin"), original),
		put(t, filepath.Join(dir, "medio.bin"), middle),
		put(t, filepath.Join(dir, "ultimo.bin"), last),
		put(t, filepath.Join(dir, "primero.bin"), first),
	)
	if len(result.Groups) != 0 {
		t.Fatalf("no debía coincidir nada: %v", groupPaths(result))
	}
}

func TestPartialHashAloneIsNotEnough(t *testing.T) {
	dir := tempDir(t)
	original := content('a')
	middle := slices.Clone(original)
	middle[testSize/2] = 'x'
	a := put(t, filepath.Join(dir, "a.bin"), original)
	b := put(t, filepath.Join(dir, "b.bin"), middle)

	partialA, err := DiskReader{}.Partial(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	partialB, err := DiskReader{}.Partial(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	if partialA != partialB {
		t.Fatal("el fixture debía tener el mismo hash parcial")
	}
}

func TestIdenticalFilesMatch(t *testing.T) {
	dir := tempDir(t)
	same := content('a')
	result := find(
		put(t, filepath.Join(dir, "uno.bin"), same),
		put(t, filepath.Join(dir, "sub", "dos.bin"), same),
		put(t, filepath.Join(dir, "tres.bin"), same),
		put(t, filepath.Join(dir, "distinto.bin"), content('b')),
	)
	got := groupPaths(result)
	if len(got) != 1 || len(got[0]) != 3 {
		t.Fatalf("grupos: %v", got)
	}
}

func TestLargestGroupsComeFirst(t *testing.T) {
	dir := tempDir(t)
	small, big := content('a'), make([]byte, 3*testSize)
	result := find(
		put(t, filepath.Join(dir, "chico1.bin"), small),
		put(t, filepath.Join(dir, "chico2.bin"), small),
		put(t, filepath.Join(dir, "grande1.bin"), big),
		put(t, filepath.Join(dir, "grande2.bin"), big),
	)
	if len(result.Groups) != 2 || result.Groups[0].Size != 3*testSize {
		t.Fatalf("%+v", result.Groups)
	}
}

// spyReader cuenta lecturas y puede fallar si alguien toca un archivo prohibido.
type spyReader struct {
	DiskReader
	partial, full atomic.Int64
	touched       func(path string)
}

func (r *spyReader) Partial(ctx context.Context, file File) (string, error) {
	r.partial.Add(1)
	if r.touched != nil {
		r.touched(file.Path)
	}
	return r.DiskReader.Partial(ctx, file)
}

func (r *spyReader) Full(ctx context.Context, file File, onBytes func(int64)) (string, error) {
	r.full.Add(1)
	if r.touched != nil {
		r.touched(file.Path)
	}
	return r.DiskReader.Full(ctx, file, onBytes)
}

func TestCloudOnlyIsNeverOpened(t *testing.T) {
	dir := tempDir(t)
	same := content('a')
	local := put(t, filepath.Join(dir, "local.bin"), same)
	other := put(t, filepath.Join(dir, "otra.bin"), same)
	cloud := put(t, filepath.Join(dir, "nube.bin"), same)
	cloud.CloudOnly = true

	reader := &spyReader{touched: func(path string) {
		if path == cloud.Path {
			t.Errorf("se abrió un archivo solo en la nube: %s", path)
		}
	}}
	result := Find(context.Background(), []File{local, other, cloud}, reader, nil, nil)
	if result.CloudFiles != 1 || result.CloudSize != testSize {
		t.Fatalf("%+v", result)
	}
	if got := groupPaths(result); len(got) != 1 || len(got[0]) != 2 {
		t.Fatalf("grupos: %v", got)
	}
}

// Sin otro archivo del mismo tamaño no hay nada que leer.
func TestUniqueSizesAreNeverRead(t *testing.T) {
	dir := tempDir(t)
	reader := &spyReader{}
	Find(context.Background(), []File{
		put(t, filepath.Join(dir, "a.bin"), make([]byte, testSize)),
		put(t, filepath.Join(dir, "b.bin"), make([]byte, testSize+1)),
	}, reader, nil, nil)
	if reader.partial.Load() != 0 || reader.full.Load() != 0 {
		t.Fatalf("leyó %d y %d", reader.partial.Load(), reader.full.Load())
	}
}

func TestOpenRefusesCloudAttributeOnRealFile(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, "offline.bin")
	put(t, path, []byte("x"))
	wide, _ := windows.UTF16PtrFromString(path)
	// FILE_ATTRIBUTE_OFFLINE es uno de los atributos de "solo en la nube", y se puede
	// poner sin proveedor. Un placeholder real queda para prueba manual.
	if err := windows.SetFileAttributes(wide, windows.FILE_ATTRIBUTE_OFFLINE); err != nil {
		t.Fatal(err)
	}
	_, _, err := openForRead(path)
	if !errors.Is(err, ErrCloudOnly) {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenRefusesLinks(t *testing.T) {
	dir := tempDir(t)
	target := filepath.Join(dir, "real.bin")
	put(t, target, []byte("x"))
	link := filepath.Join(dir, "enlace.bin")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink sin permiso: %v", err)
	}
	if _, _, err := openForRead(link); !errors.Is(err, ErrLink) {
		t.Fatalf("err = %v", err)
	}
}

func TestLockedFileIsCountedAndDoesNotStop(t *testing.T) {
	dir := tempDir(t)
	same := content('a')
	locked := put(t, filepath.Join(dir, "bloqueado.bin"), same)
	a := put(t, filepath.Join(dir, "a.bin"), same)
	b := put(t, filepath.Join(dir, "b.bin"), same)

	wide, _ := windows.UTF16PtrFromString(locked.Path)
	handle, err := windows.CreateFile(wide, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)

	result := find(locked, a, b)
	if result.Unreadable != 1 {
		t.Fatalf("Unreadable = %d", result.Unreadable)
	}
	if got := groupPaths(result); len(got) != 1 || len(got[0]) != 2 {
		t.Fatalf("grupos: %v", got)
	}
}

func TestChangedSizeIsNotHashed(t *testing.T) {
	dir := tempDir(t)
	file := put(t, filepath.Join(dir, "a.bin"), content('a'))
	file.Size++
	if _, err := (DiskReader{}).Partial(context.Background(), file); err == nil {
		t.Fatal("debía rechazarlo")
	}
	if _, err := (DiskReader{}).Full(context.Background(), file, nil); err == nil {
		t.Fatal("debía rechazarlo")
	}
}

func TestCancelKeepsWhatItFound(t *testing.T) {
	dir := tempDir(t)
	big, small := make([]byte, 2*testSize), content('a')
	files := []File{
		put(t, filepath.Join(dir, "g1.bin"), big),
		put(t, filepath.Join(dir, "g2.bin"), big),
		put(t, filepath.Join(dir, "c1.bin"), small),
		put(t, filepath.Join(dir, "c2.bin"), small),
	}

	ctx, cancel := context.WithCancel(context.Background())
	reader := &cancelReader{cancelAfter: 2, cancel: cancel}
	result := Find(ctx, files, reader, nil, nil)
	if !result.Canceled {
		t.Fatal("debía quedar como cancelada")
	}
	if len(result.Groups) != 1 || result.Groups[0].Size != 2*testSize {
		t.Fatalf("debía conservar el grupo grande: %v", groupPaths(result))
	}
}

// cancelReader cancela tras terminar cierto número de hashes completos, en orden.
type cancelReader struct {
	DiskReader
	cancelAfter int64
	full        atomic.Int64
	cancel      func()
}

func (r *cancelReader) Full(ctx context.Context, file File, onBytes func(int64)) (string, error) {
	if r.full.Load() >= r.cancelAfter {
		return "", context.Canceled
	}
	hash, err := r.DiskReader.Full(ctx, file, onBytes)
	if r.full.Add(1) >= r.cancelAfter {
		r.cancel()
	}
	return hash, err
}

func TestCacheAvoidsRereading(t *testing.T) {
	dir := tempDir(t)
	same := content('a')
	files := []File{
		put(t, filepath.Join(dir, "a.bin"), same),
		put(t, filepath.Join(dir, "b.bin"), same),
	}
	dbPath := filepath.Join(dir, "cache.db")

	first := &spyReader{}
	cache := OpenCache(dbPath)
	Find(context.Background(), files, first, cache, nil)
	cache.Close()
	if first.full.Load() != 2 {
		t.Fatalf("primera vez: %d", first.full.Load())
	}

	second := &spyReader{}
	cache = OpenCache(dbPath)
	defer cache.Close()
	result := Find(context.Background(), files, second, cache, nil)
	if second.partial.Load() != 0 || second.full.Load() != 0 {
		t.Fatalf("releyó: %d, %d", second.partial.Load(), second.full.Load())
	}
	if len(result.Groups) != 1 {
		t.Fatalf("%v", groupPaths(result))
	}
}

func TestCacheInvalidatesWhenFileChanges(t *testing.T) {
	dir := tempDir(t)
	dbPath := filepath.Join(dir, "cache.db")
	same := content('a')
	a := put(t, filepath.Join(dir, "a.bin"), same)
	b := put(t, filepath.Join(dir, "b.bin"), same)

	cache := OpenCache(dbPath)
	Find(context.Background(), []File{a, b}, DiskReader{}, cache, nil)
	cache.Close()

	// Mismo tamaño, otro contenido, fecha distinta: la caché no debe confirmar la copia.
	different := slices.Clone(same)
	different[testSize/2] = 'x'
	b = put(t, b.Path, different)
	b.Modified = a.Modified + 1

	reader := &spyReader{}
	cache = OpenCache(dbPath)
	defer cache.Close()
	result := Find(context.Background(), []File{a, b}, reader, cache, nil)
	if len(result.Groups) != 0 {
		t.Fatalf("usó un hash viejo: %v", groupPaths(result))
	}
	if reader.full.Load() != 1 {
		t.Fatalf("debía releer solo el modificado: %d", reader.full.Load())
	}
}

func TestCacheKeepsPartialWhenAddingFull(t *testing.T) {
	dir := tempDir(t)
	cache := OpenCache(filepath.Join(dir, "cache.db"))
	defer cache.Close()
	file := File{Path: `C:\x.bin`, Size: 10, Modified: 1}
	cache.putPartial(file, "parcial")
	cache.flush()
	cache.putFull(file, "completo")
	cache.flush()
	if got, ok := cache.partial(file); !ok || got != "parcial" {
		t.Fatalf("parcial: %q, %v", got, ok)
	}
	if got, ok := cache.full(file); !ok || got != "completo" {
		t.Fatalf("completo: %q, %v", got, ok)
	}

	changed := File{Path: file.Path, Size: 11, Modified: 1}
	cache.putPartial(changed, "nuevo")
	cache.flush()
	if _, ok := cache.full(changed); ok {
		t.Fatal("conservó un hash de otra versión del archivo")
	}
}

func TestCorruptCacheIsRecreated(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, "cache.db")
	if err := os.WriteFile(path, []byte("esto no es una base de datos, de verdad que no lo es"), 0o644); err != nil {
		t.Fatal(err)
	}
	cache := OpenCache(path)
	if cache == nil {
		t.Fatal("debía recrearla")
	}
	defer cache.Close()
	file := File{Path: `C:\x.bin`, Size: 1, Modified: 1}
	cache.putFull(file, "h")
	cache.flush()
	if got, ok := cache.full(file); !ok || got != "h" {
		t.Fatalf("%q, %v", got, ok)
	}
}

func TestNilCacheStillWorks(t *testing.T) {
	var cache *Cache
	cache.putFull(File{}, "x")
	cache.flush()
	cache.Close()
	if _, ok := cache.full(File{}); ok {
		t.Fatal("una caché nil no tiene nada")
	}
}

func wantCode(t *testing.T, err error, want actions.Code) {
	t.Helper()
	if got := actions.CodeOf(err); got != want {
		t.Fatalf("código = %q (%v), se esperaba %q", got, err, want)
	}
}

func verifyFixture(t *testing.T) (keep, other File, hash string) {
	t.Helper()
	dir := tempDir(t)
	same := content('a')
	keep = put(t, filepath.Join(dir, "conservar.bin"), same)
	other = put(t, filepath.Join(dir, "copia.bin"), same)
	hash, err := DiskReader{}.Full(context.Background(), keep, nil)
	if err != nil {
		t.Fatal(err)
	}
	return keep, other, hash
}

func TestVerifyAcceptsRealCopy(t *testing.T) {
	keep, other, hash := verifyFixture(t)
	if err := VerifyCopy(context.Background(), keep.Path, other.Path, hash, keep.Size); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyRefusesKeepingWhatItDeletes(t *testing.T) {
	keep, _, hash := verifyFixture(t)
	wantCode(t, VerifyCopy(context.Background(), keep.Path, keep.Path, hash, keep.Size), actions.CodeNoCopyLeft)
	wantCode(t, VerifyCopy(context.Background(), keep.Path, filepath.Join(filepath.Dir(keep.Path), "CONSERVAR.BIN"), hash, keep.Size), actions.CodeNoCopyLeft)
}

func TestVerifyRefusesWhenKeepIsGone(t *testing.T) {
	keep, other, hash := verifyFixture(t)
	if err := os.Remove(keep.Path); err != nil {
		t.Fatal(err)
	}
	wantCode(t, VerifyCopy(context.Background(), keep.Path, other.Path, hash, keep.Size), actions.CodeNoCopyLeft)
}

func TestVerifyRefusesWhenKeepChangedKeepingSize(t *testing.T) {
	keep, other, hash := verifyFixture(t)
	changed := content('a')
	changed[testSize/2] = 'x'
	put(t, keep.Path, changed)
	wantCode(t, VerifyCopy(context.Background(), keep.Path, other.Path, hash, keep.Size), actions.CodeNoCopyLeft)
}

func TestVerifyRefusesWhenCopyChangedKeepingSize(t *testing.T) {
	keep, other, hash := verifyFixture(t)
	changed := content('a')
	changed[testSize/2] = 'x'
	put(t, other.Path, changed)
	wantCode(t, VerifyCopy(context.Background(), keep.Path, other.Path, hash, keep.Size), actions.CodeNotDuplicate)
}

func TestVerifyRefusesHardLink(t *testing.T) {
	keep, _, hash := verifyFixture(t)
	link := filepath.Join(filepath.Dir(keep.Path), "hardlink.bin")
	if err := os.Link(keep.Path, link); err != nil {
		t.Skipf("hard link no disponible: %v", err)
	}
	wantCode(t, VerifyCopy(context.Background(), keep.Path, link, hash, keep.Size), actions.CodeNotDuplicate)
}

func TestVerifyRefusesKeepInRecycleBin(t *testing.T) {
	_, other, hash := verifyFixture(t)
	wantCode(t, VerifyCopy(context.Background(), `C:\$Recycle.Bin\S-1-5\$Rabc.bin`, other.Path, hash, other.Size), actions.CodeNoCopyLeft)
}

func TestVerifyCanceled(t *testing.T) {
	keep, other, hash := verifyFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	wantCode(t, VerifyCopy(ctx, keep.Path, other.Path, hash, keep.Size), actions.CodeCanceled)
}
