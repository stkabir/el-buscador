package scanner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

const mib = 1024 * 1024

func writeFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

func scan(t *testing.T, root string) *Result {
	t.Helper()
	result, err := Scan(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func junction(t *testing.T, link, target string) {
	t.Helper()
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J: %v: %s", err, out)
	}
}

func TestScanSumsFilesAcrossFolders(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.bin"), mib)
	writeFile(t, filepath.Join(root, "sub", "b.bin"), 2*mib)
	writeFile(t, filepath.Join(root, "sub", "deep", "c.bin"), 3*mib)

	summary := scan(t, root).Summary()
	if summary.Files != 3 {
		t.Errorf("Files = %d, quiero 3", summary.Files)
	}
	if summary.Dirs != 3 {
		t.Errorf("Dirs = %d, quiero 3", summary.Dirs)
	}
	if summary.Size < 6*mib {
		t.Errorf("Size = %d, quiero al menos %d", summary.Size, 6*mib)
	}
	if summary.TopFiles[0].Name != "c.bin" {
		t.Errorf("archivo mayor = %s, quiero c.bin", summary.TopFiles[0].Name)
	}
}

func TestScanCountsHardLinksOnce(t *testing.T) {
	root := t.TempDir()
	original := filepath.Join(root, "original.bin")
	writeFile(t, original, 4*mib)
	if err := os.Mkdir(filepath.Join(root, "otra"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(original, filepath.Join(root, "otra", "enlace.bin")); err != nil {
		t.Fatal(err)
	}

	summary := scan(t, root).Summary()
	if summary.Files != 1 || summary.HardLinks != 1 {
		t.Errorf("Files = %d, HardLinks = %d, quiero 1 y 1", summary.Files, summary.HardLinks)
	}
	if summary.Size >= 8*mib {
		t.Errorf("Size = %d, hard link contado dos veces", summary.Size)
	}
}

func TestScanSkipsJunctions(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeFile(t, filepath.Join(root, "a.bin"), mib)
	writeFile(t, filepath.Join(outside, "fuera.bin"), 5*mib)
	junction(t, filepath.Join(root, "afuera"), outside)
	junction(t, filepath.Join(root, "ciclo"), root)

	summary := scan(t, root).Summary()
	if summary.Files != 1 {
		t.Errorf("Files = %d, quiero 1", summary.Files)
	}
	if summary.Links != 2 {
		t.Errorf("Links = %d, quiero 2", summary.Links)
	}
}

func TestScanSkipsSymlinks(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "real.bin")
	writeFile(t, target, 2*mib)
	if err := os.Symlink(target, filepath.Join(root, "enlace.bin")); err != nil {
		t.Skipf("symlinks requieren permiso: %v", err)
	}

	summary := scan(t, root).Summary()
	if summary.Files != 1 || summary.Links != 1 {
		t.Errorf("Files = %d, Links = %d, quiero 1 y 1", summary.Files, summary.Links)
	}
}

func TestScanHandlesLongPathsAndUnicode(t *testing.T) {
	root := t.TempDir()
	path := root
	for len(path) < 300 {
		path = filepath.Join(path, strings.Repeat("carpeta-año-日本", 2))
	}
	writeFile(t, filepath.Join(path, "archivo ñ 日本.bin"), mib)

	summary := scan(t, root).Summary()
	if summary.Unreadable != 0 {
		t.Errorf("Unreadable = %d: %v", summary.Unreadable, summary.Samples)
	}
	if summary.Files != 1 || summary.TopFiles[0].Name != "archivo ñ 日本.bin" {
		t.Errorf("Files = %d, top = %+v", summary.Files, summary.TopFiles)
	}
}

func TestScanReportsUnreadableFolders(t *testing.T) {
	root := t.TempDir()
	locked := filepath.Join(root, "bloqueada")
	writeFile(t, filepath.Join(locked, "secreto.bin"), mib)
	const everyone = "*S-1-1-0"
	if out, err := exec.Command("icacls", locked, "/deny", everyone+":(OI)(CI)(F)").CombinedOutput(); err != nil {
		t.Fatalf("icacls: %v: %s", err, out)
	}
	t.Cleanup(func() {
		if out, err := exec.Command("icacls", locked, "/remove:d", everyone).CombinedOutput(); err != nil {
			t.Errorf("restaurar permisos: %v: %s", err, out)
		}
	})

	result := scan(t, root)
	summary := result.Summary()
	if summary.Unreadable != 1 || summary.Files != 0 {
		t.Errorf("Unreadable = %d, Files = %d, quiero 1 y 0", summary.Unreadable, summary.Files)
	}
	listing, err := result.Listing(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(listing.Dirs) != 1 || !listing.Dirs[0].Unreadable {
		t.Errorf("Dirs = %+v, quiero bloqueada sin acceso", listing.Dirs)
	}
}

func TestScanHonorsCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Scan(ctx, t.TempDir(), nil); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, quiero context.Canceled", err)
	}
}

func TestScanLimitsTopFiles(t *testing.T) {
	root := t.TempDir()
	for i := range topFilesLimit + 5 {
		writeFile(t, filepath.Join(root, fmt.Sprintf("%03d.bin", i)), 8192*(i+1))
	}

	files := scan(t, root).Summary().TopFiles
	if len(files) != topFilesLimit {
		t.Fatalf("len = %d, quiero %d", len(files), topFilesLimit)
	}
	if files[0].Name != fmt.Sprintf("%03d.bin", topFilesLimit+4) {
		t.Errorf("primero = %s", files[0].Name)
	}
	for i := 1; i < len(files); i++ {
		if files[i].Size > files[i-1].Size {
			t.Fatalf("sin orden en posición %d", i)
		}
	}
	if files[len(files)-1].Name != "005.bin" {
		t.Errorf("último = %s, quiero 005.bin", files[len(files)-1].Name)
	}
}

func TestTopDirsOmitsPassThroughFolders(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "paso", "real", "grande.bin"), 4*mib)
	writeFile(t, filepath.Join(root, "otra", "mediano.bin"), mib)

	var names []string
	for _, dir := range scan(t, root).Summary().TopDirs {
		names = append(names, dir.Name)
	}
	if got := strings.Join(names, ","); got != "real,otra" {
		t.Errorf("TopDirs = %s, quiero real,otra", got)
	}
}

func TestListingNavigatesAndSummarizesFiles(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Sub")
	for i := range dirFilesLimit + 3 {
		writeFile(t, filepath.Join(dir, fmt.Sprintf("%02d.bin", i)), 8192*(i+1))
	}

	result := scan(t, root)
	listing, err := result.Listing(strings.ToLower(dir))
	if err != nil {
		t.Fatal(err)
	}
	if len(listing.FileItems) != dirFilesLimit || listing.OtherFiles != 3 {
		t.Errorf("FileItems = %d, OtherFiles = %d", len(listing.FileItems), listing.OtherFiles)
	}
	if listing.Files != int64(dirFilesLimit+3) {
		t.Errorf("Files = %d", listing.Files)
	}
	if !strings.EqualFold(listing.Parent, root) {
		t.Errorf("Parent = %s, quiero %s", listing.Parent, root)
	}

	rootListing, err := result.Listing(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rootListing.Dirs) != 1 || rootListing.Parent != "" {
		t.Errorf("raíz: %+v", rootListing)
	}

	if _, err := result.Listing(filepath.Join(root, "no-existe")); err == nil {
		t.Error("carpeta inexistente sin error")
	}
	if _, err := result.Listing(`Z:\fuera`); err == nil {
		t.Error("ruta fuera de la raíz sin error")
	}
}

func TestLastActivityIgnoresExcludedFolders(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "app.js")
	trash := filepath.Join(root, "node_modules", "dep.js")
	writeFile(t, source, 8192)
	writeFile(t, trash, 8192)
	old := time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(source, old, old); err != nil {
		t.Fatal(err)
	}

	result := scan(t, root)
	exclude := func(name string) bool { return name == "node_modules" }
	if got := result.LastActivity(root, exclude); got != old.Unix() {
		t.Errorf("LastActivity = %d, quiero %d", got, old.Unix())
	}
	keepAll := func(string) bool { return false }
	if got := result.LastActivity(root, keepAll); got <= old.Unix() {
		t.Errorf("LastActivity sin exclusión = %d, quiero más de %d", got, old.Unix())
	}
}

func TestWalkDirsStopsWhenVisitReturnsFalse(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "alto", "adentro", "a.bin"), 8192)
	writeFile(t, filepath.Join(root, "sigue", "adentro", "b.bin"), 8192)

	var seen []string
	scan(t, root).WalkDirs(func(entry Entry) bool {
		seen = append(seen, strings.TrimPrefix(entry.Path, root+`\`))
		return entry.Name != "alto"
	})
	slices.Sort(seen)
	if got := strings.Join(seen, ","); got != `alto,sigue,sigue\adentro` {
		t.Errorf("visitadas = %s", got)
	}
}

func TestClassify(t *testing.T) {
	const (
		dir     = windows.FILE_ATTRIBUTE_DIRECTORY
		reparse = windows.FILE_ATTRIBUTE_REPARSE_POINT
	)
	tests := []struct {
		name  string
		attrs uint32
		tag   uint32
		want  entryKind
	}{
		{"archivo normal", windows.FILE_ATTRIBUTE_ARCHIVE, 0, kindFile},
		{"carpeta normal", dir, 0, kindDir},
		{"junction", dir | reparse, windows.IO_REPARSE_TAG_MOUNT_POINT, kindLink},
		{"symlink a carpeta", dir | reparse, windows.IO_REPARSE_TAG_SYMLINK, kindLink},
		{"carpeta nube", dir | reparse, 0x9000001A, kindDir},
		{"carpeta nube variante", dir | reparse, 0x9000701A, kindDir},
		{"carpeta OneDrive", dir | reparse, reparseTagOneDrive, kindDir},
		{"carpeta etiqueta desconocida", dir | reparse, 0x80000099, kindLink},
		{"symlink a archivo", reparse, windows.IO_REPARSE_TAG_SYMLINK, kindLink},
		{"archivo nube", reparse, 0x9000001A, kindFile},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := classify(test.attrs, test.tag); got != test.want {
				t.Errorf("classify = %d, quiero %d", got, test.want)
			}
		})
	}
}

func TestMeasure(t *testing.T) {
	tests := []struct {
		name               string
		attrs              uint32
		endOfFile, alloc   int64
		wantSize, wantNube int64
	}{
		{"local", windows.FILE_ATTRIBUTE_ARCHIVE, 100, 4096, 4096, 0},
		{"solo nube", windows.FILE_ATTRIBUTE_RECALL_ON_DATA_ACCESS, 5000, 0, 0, 5000},
		{"descarga parcial", windows.FILE_ATTRIBUTE_RECALL_ON_DATA_ACCESS, 5000, 1000, 1000, 4000},
		{"offline", windows.FILE_ATTRIBUTE_OFFLINE, 5000, 0, 0, 5000},
		{"sparse local", windows.FILE_ATTRIBUTE_SPARSE_FILE, 5000, 1000, 1000, 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			size, cloud := measure(test.attrs, test.endOfFile, test.alloc)
			if size != test.wantSize || cloud != test.wantNube {
				t.Errorf("measure = %d, %d; quiero %d, %d", size, cloud, test.wantSize, test.wantNube)
			}
		})
	}
}

// TestRealScan compara contra un disco real. Solo corre con la variable
// EL_BUSCADOR_REAL_SCAN, y solo lee.
func TestRealScan(t *testing.T) {
	root := os.Getenv("EL_BUSCADOR_REAL_SCAN")
	if root == "" {
		t.Skip("define EL_BUSCADOR_REAL_SCAN con una ruta")
	}

	result := scan(t, root)
	summary := result.Summary()
	const gib = 1024 * mib
	t.Logf("%.1f GB en disco, %.1f GB solo nube, %d archivos, %d carpetas, %.1f s",
		float64(summary.Size)/gib, float64(summary.Cloud)/gib, summary.Files, summary.Dirs, float64(summary.DurationMs)/1000)
	t.Logf("hard links %d, enlaces %d, sin leer %d, admin %v", summary.HardLinks, summary.Links, summary.Unreadable, summary.Elevated)
	for _, sample := range summary.Samples {
		t.Logf("sin leer: %s", sample)
	}
	for _, file := range summary.TopFiles[:min(10, len(summary.TopFiles))] {
		t.Logf("archivo %8.2f GB  %s", float64(file.Size)/gib, file.Path)
	}
	for _, dir := range summary.TopDirs[:min(15, len(summary.TopDirs))] {
		t.Logf("carpeta %8.2f GB (+%.1f nube)  %s", float64(dir.Size)/gib, float64(dir.Cloud)/gib, dir.Path)
	}
	for _, name := range []string{"iCloudPhotos", "iCloudDrive", "OneDrive"} {
		path := filepath.Join(os.Getenv("USERPROFILE"), name)
		if listing, err := result.Listing(path); err != nil {
			t.Logf("%s: %v", name, err)
		} else {
			t.Logf("%s: %.1f GB en disco, %.1f GB solo nube, %d archivos", name, float64(listing.Size)/gib, float64(listing.Cloud)/gib, listing.Files)
		}
	}
}

func TestFindLocatesFilesAndFolders(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "carpeta")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(folder, "dato.bin")
	if err := os.WriteFile(file, make([]byte, 8192), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Scan(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}

	if entry, ok := result.Find(folder); !ok || !entry.IsDir {
		t.Fatalf("carpeta: %+v, %v", entry, ok)
	}
	if entry, ok := result.Find(file); !ok || entry.IsDir || entry.Size == 0 {
		t.Fatalf("archivo: %+v, %v", entry, ok)
	}
	if _, ok := result.Find(filepath.Join(folder, "no-existe")); ok {
		t.Fatal("encontró un archivo inexistente")
	}
}

func TestTreeLevelsOrderAndOther(t *testing.T) {
	root := t.TempDir()
	write := func(rel string, size int) {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("grande/sub/profundo.bin", 2*1024*1024)
	write("grande/otra/igual.bin", 2*1024*1024)
	write("chica/a.bin", 1024*1024)
	for i := range maxTreeChildren + 5 {
		write(filepath.Join("muchas", "d"+string(rune('a'+i%26))+string(rune('a'+i/26)), "x.bin"), 8192)
	}
	result, err := Scan(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}

	tree, parent, err := result.Tree(root, 2)
	if err != nil {
		t.Fatal(err)
	}
	if parent != "" {
		t.Fatalf("la raíz no tiene padre: %q", parent)
	}
	if len(tree.Children) != 3 || tree.Children[0].Name != "grande" {
		t.Fatalf("%+v", tree.Children)
	}
	// El segundo nivel existe; el tercero ya no se manda.
	sub := tree.Children[0].Children[0]
	if !sub.IsDir || len(sub.Children) != 0 || sub.Size == 0 {
		t.Fatalf("%+v", sub)
	}
	// Lo que no se lista no se pierde: la suma sigue siendo el total.
	var many TreeNode
	for _, child := range tree.Children {
		if child.Name == "muchas" {
			many = child
		}
	}
	if len(many.Children) != maxTreeChildren || many.Other == 0 {
		t.Fatalf("hijos %d, otros %d", len(many.Children), many.Other)
	}
	var sum int64 = many.Other
	for _, child := range many.Children {
		sum += child.Size
	}
	if sum != many.Size {
		t.Fatalf("suma %d, total %d", sum, many.Size)
	}

	if _, _, err := result.Tree(filepath.Join(root, "no-existe"), 2); err == nil {
		t.Fatal("debía fallar")
	}
	_, parent, err = result.Tree(filepath.Join(root, "grande"), 1)
	if err != nil || parent != root {
		t.Fatalf("padre %q, %v", parent, err)
	}
}

// Una cadena de carpetas de paso no gasta los niveles del mapa.
func TestTreePassThroughKeepsDepth(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"Users/ana/fotos/a.bin", "Users/ana/series/b.bin"} {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, make([]byte, 1024*1024), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	result, err := Scan(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	tree, _, err := result.Tree(root, 2)
	if err != nil {
		t.Fatal(err)
	}
	ana := tree.Children[0].Children[0]
	if ana.Name != "ana" || len(ana.Children) != 2 {
		t.Fatalf("debía llegar a lo que hay en ana: %+v", ana)
	}
}
