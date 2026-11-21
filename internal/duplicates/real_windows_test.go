package duplicates_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"el-buscador/internal/devtrash"
	"el-buscador/internal/disk"
	"el-buscador/internal/duplicates"
	"el-buscador/internal/scanner"
)

// Búsqueda real, solo lectura. Se activa con EL_BUSCADOR_REAL_SCAN='C:\'. Nunca borra:
// este test no usa nada de limpieza. EL_BUSCADOR_REAL_LIMIT limita el tiempo (por
// omisión 8 minutos); al llegar se cancela y reporta lo alcanzado.
func TestRealSearch(t *testing.T) {
	root := os.Getenv("EL_BUSCADOR_REAL_SCAN")
	if root == "" {
		t.Skip("define EL_BUSCADOR_REAL_SCAN")
	}
	limit := 8 * time.Minute
	if value, err := time.ParseDuration(os.Getenv("EL_BUSCADOR_REAL_LIMIT")); err == nil {
		limit = value
	}
	before, err := disk.System()
	if err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	result, err := scanner.ScanWith(context.Background(), root, nil, scanner.Options{SkipCandidates: devtrash.SkipForDuplicates})
	if err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	candidates := result.Candidates()
	t.Logf("escaneo: %s, %d candidatos, memoria %d MB", time.Since(started).Round(time.Second), len(candidates), memory.HeapAlloc/1024/1024)

	files := make([]duplicates.File, len(candidates))
	for i, c := range candidates {
		files[i] = duplicates.File{Path: c.Path(), Size: c.Size, Allocated: c.Allocated, Modified: c.Modified, CloudOnly: c.CloudOnly}
	}

	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	cache := duplicates.OpenCache(filepath.Join(t.TempDir(), "hashes.db"))
	defer cache.Close()

	tracker := &duplicates.Tracker{}
	searchStart := time.Now()
	found := duplicates.Find(ctx, files, duplicates.DiskReader{}, cache, tracker)

	var waste int64
	for _, group := range found.Groups {
		waste += group.Allocated * int64(len(group.Files)-1)
	}
	progress := tracker.Snapshot()
	after, err := disk.System()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("búsqueda: %s, cancelada=%v, fase=%s %d/%d, leídos %d MB", time.Since(searchStart).Round(time.Second), found.Canceled, progress.Phase, progress.Done, progress.Total, progress.Bytes/1024/1024)
	t.Logf("grupos: %d, recuperable %d MB", len(found.Groups), waste/1024/1024)
	t.Logf("sin comparar: nube %d archivos (%d MB), ilegibles %d", found.CloudFiles, found.CloudSize/1024/1024, found.Unreadable)
	// Una bajada grande sugeriría una descarga de la nube; hay otros programas usando
	// el disco, así que es un indicio y no una prueba.
	t.Logf("disco libre: antes %d MB, después %d MB", before.Free/1024/1024, after.Free/1024/1024)
}
