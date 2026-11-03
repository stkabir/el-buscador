package actions

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func never() bool { return false }

// tempDir devuelve una carpeta temporal de test con su ruta real, sin nombres
// cortos del sistema (KABIR~1).
func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// restore saca de la papelera lo que mandó el test, con el mismo mecanismo que usa
// el usuario. Así el test no deja nada en la papelera real.
func restore(t *testing.T, recycled string) {
	t.Helper()
	script := fmt.Sprintf(`
$bin = (New-Object -ComObject Shell.Application).Namespace(10)
$item = $bin.Items() | Where-Object { $_.Path -eq '%s' }
if (-not $item) { exit 3 }
$item.InvokeVerb('undelete')`, strings.ReplaceAll(recycled, "'", "''"))
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	if err != nil {
		t.Fatalf("restaurar %q: %v: %s", recycled, err, out)
	}
}

func TestMoveToTrashFileAndRestore(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, "datos con espacios ñ.txt")
	writeFile(t, path, "contenido")

	recycled, err := MoveToTrash(path, never)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("el original sigue ahí: %v", err)
	}
	if !strings.Contains(strings.ToLower(recycled), `$recycle.bin`) {
		t.Fatalf("no quedó en la papelera: %q", recycled)
	}
	got, err := os.ReadFile(recycled)
	if err != nil || string(got) != "contenido" {
		t.Fatalf("copia en papelera: %q, %v", got, err)
	}

	restore(t, recycled)
	got, err = os.ReadFile(path)
	if err != nil || string(got) != "contenido" {
		t.Fatalf("tras restaurar: %q, %v", got, err)
	}
}

func TestMoveToTrashFolderAndRestore(t *testing.T) {
	dir := tempDir(t)
	folder := filepath.Join(dir, "carpeta")
	writeFile(t, filepath.Join(folder, "a", "b.txt"), "b")
	writeFile(t, filepath.Join(folder, "c.txt"), "c")

	recycled, err := MoveToTrash(folder, never)
	if err != nil {
		t.Fatal(err)
	}
	restore(t, recycled)
	got, err := os.ReadFile(filepath.Join(folder, "a", "b.txt"))
	if err != nil || string(got) != "b" {
		t.Fatalf("tras restaurar: %q, %v", got, err)
	}
}

func TestMoveToTrashCanceledTouchesNothing(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, "x.txt")
	writeFile(t, path, "x")

	_, err := MoveToTrash(path, func() bool { return true })
	if !errors.Is(err, ErrCanceled) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("debía seguir ahí: %v", err)
	}
}

func TestMoveToTrashInUse(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, "abierto.txt")
	writeFile(t, path, "x")

	wide, _ := windows.UTF16PtrFromString(path)
	// Sin compartir nada: nadie más puede abrirlo ni moverlo.
	handle, err := windows.CreateFile(wide, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)

	_, err = MoveToTrash(path, never)
	if err == nil {
		t.Fatal("no debía poder moverlo")
	}
	if !errors.Is(err, ErrInUse) {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Lstat(path); statErr != nil {
		t.Fatalf("debía seguir ahí: %v", statErr)
	}
}

// Lo normal en node_modules: un archivo bloqueado dentro de la carpeta. No puede
// quedar a medias: o se mueve completa o se queda completa.
func TestMoveToTrashFolderWithLockedChild(t *testing.T) {
	dir := tempDir(t)
	folder := filepath.Join(dir, "carpeta")
	locked := filepath.Join(folder, "abierto.txt")
	other := filepath.Join(folder, "sub", "otro.txt")
	writeFile(t, locked, "x")
	writeFile(t, other, "y")

	handle, err := windows.CreateFile(mustUTF16(t, locked), windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)

	recycled, err := MoveToTrash(folder, never)
	if err == nil {
		restore(t, recycled)
		t.Fatal("no debía poder moverla")
	}
	t.Logf("error: %v", err)
	for _, path := range []string{locked, other} {
		if _, statErr := os.Lstat(path); statErr != nil {
			t.Errorf("%s desapareció: %v", path, statErr)
		}
	}
}

// Una carpeta demasiado profunda no cabe en la papelera, y es el rechazo que se
// puede provocar sin tocar la configuración del usuario. Lo crucial: si Windows no
// la recicla, tiene que seguir completa en su lugar.
func TestMoveToTrashNeverDeletesPermanently(t *testing.T) {
	dir := tempDir(t)
	top := filepath.Join(dir, "profunda")
	long := `\\?\` + top
	if err := windows.CreateDirectory(mustUTF16(t, long), nil); err != nil {
		t.Fatal(err)
	}
	for range 12 {
		long += `\` + strings.Repeat("d", 40)
		if err := windows.CreateDirectory(mustUTF16(t, long), nil); err != nil {
			t.Fatal(err)
		}
	}
	file := long + `\archivo.txt`
	handle, err := windows.CreateFile(mustUTF16(t, file), windows.GENERIC_WRITE, 0, nil, windows.CREATE_NEW, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	windows.CloseHandle(handle)

	if recycled, err := MoveToTrash(top, never); !errors.Is(err, ErrWouldDeletePermanently) {
		if err == nil {
			restore(t, recycled)
		}
		t.Fatalf("err = %v", err)
	}
	if _, statErr := windows.GetFileAttributes(mustUTF16(t, file)); statErr != nil {
		t.Fatalf("el archivo desapareció: %v", statErr)
	}
}

func mustUTF16(t *testing.T, s string) *uint16 {
	t.Helper()
	p, err := windows.UTF16PtrFromString(s)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
