// Package duplicates encuentra archivos idénticos. Compara primero por tamaño, luego
// por un hash parcial y al final por el hash de todo el contenido. Nunca borra: solo
// lee, y nunca abre archivos que están solo en la nube.
package duplicates

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
)

const (
	partialWorkers = 4
	// Pocos lectores a la vez: en un disco mecánico, más lectores van más lento.
	fullWorkers = 2

	PhaseIdle    = "idle"
	PhasePartial = "partial"
	PhaseFull    = "full"
)

// File es un archivo por comparar.
type File struct {
	Path      string
	Size      int64
	Allocated int64
	Modified  int64
	CloudOnly bool
}

// Group son archivos idénticos. Hash identifica el contenido.
type Group struct {
	Hash      string
	Size      int64
	Allocated int64
	Files     []File
}

// Result es lo que encontró una búsqueda.
type Result struct {
	Groups []Group
	// Canceled indica que se detuvo antes de terminar: Groups trae solo lo alcanzado.
	Canceled bool
	// CloudFiles y CloudSize son lo que no se comparó por estar solo en la nube.
	CloudFiles int64
	CloudSize  int64
	// Unreadable son los archivos que no se pudieron leer, por ejemplo por estar en uso.
	Unreadable int64
}

// Progress es una foto del avance.
type Progress struct {
	Phase     string `json:"phase"`
	Done      int64  `json:"done"`
	Total     int64  `json:"total"`
	Bytes     int64  `json:"bytes"`
	BytesGoal int64  `json:"bytesGoal"`
	Current   string `json:"current"`
}

// Tracker expone el avance de una búsqueda en curso.
type Tracker struct {
	done, total, bytes, goal atomic.Int64
	phase, current           atomic.Pointer[string]
}

func (t *Tracker) startPhase(phase string, total, goal int64) {
	t.phase.Store(&phase)
	t.done.Store(0)
	t.bytes.Store(0)
	t.total.Store(total)
	t.goal.Store(goal)
}

// Snapshot devuelve el avance actual.
func (t *Tracker) Snapshot() Progress {
	progress := Progress{
		Phase: PhaseIdle, Done: t.done.Load(), Total: t.total.Load(),
		Bytes: t.bytes.Load(), BytesGoal: t.goal.Load(),
	}
	if phase := t.phase.Load(); phase != nil {
		progress.Phase = *phase
	}
	if current := t.current.Load(); current != nil {
		progress.Current = *current
	}
	return progress
}

// Reader lee el contenido de los archivos. Se reemplaza en tests.
type Reader interface {
	Partial(ctx context.Context, file File) (string, error)
	Full(ctx context.Context, file File, onBytes func(int64)) (string, error)
}

// Find busca copias entre files. Lo que se alcanzó a calcular sobrevive a una
// cancelación, porque queda en la caché y en el resultado.
func Find(ctx context.Context, files []File, reader Reader, cache *Cache, tracker *Tracker) Result {
	if tracker == nil {
		tracker = &Tracker{}
	}
	var result Result
	bySize := make(map[int64][]File)
	for _, file := range files {
		if file.CloudOnly {
			result.CloudFiles++
			result.CloudSize += file.Size
			continue
		}
		if file.Size > 0 {
			bySize[file.Size] = append(bySize[file.Size], file)
		}
	}

	var unreadable atomic.Int64
	var pending []File
	for _, same := range bySize {
		if len(same) > 1 {
			pending = append(pending, same...)
		}
	}
	// Lo más grande primero: si se cancela, ya se vio lo que más espacio libera.
	slices.SortFunc(pending, bySizeDesc)

	tracker.startPhase(PhasePartial, int64(len(pending)), 0)
	partial := hashAll(ctx, pending, partialWorkers, tracker, &unreadable, func(ctx context.Context, file File) (string, error) {
		if hash, ok := cache.partial(file); ok {
			return hash, nil
		}
		hash, err := reader.Partial(ctx, file)
		if err == nil {
			cache.putPartial(file, hash)
		}
		return hash, err
	})

	type key struct {
		size int64
		hash string
	}
	byPartial := make(map[key][]File)
	for _, file := range pending {
		if hash, ok := partial[file.Path]; ok {
			k := key{file.Size, hash}
			byPartial[k] = append(byPartial[k], file)
		}
	}
	var candidates []File
	var goal int64
	for _, same := range byPartial {
		if len(same) > 1 {
			candidates = append(candidates, same...)
			for _, file := range same {
				goal += file.Size
			}
		}
	}
	slices.SortFunc(candidates, bySizeDesc)

	tracker.startPhase(PhaseFull, int64(len(candidates)), goal)
	full := hashAll(ctx, candidates, fullWorkers, tracker, &unreadable, func(ctx context.Context, file File) (string, error) {
		if hash, ok := cache.full(file); ok {
			tracker.bytes.Add(file.Size)
			return hash, nil
		}
		hash, err := reader.Full(ctx, file, func(n int64) { tracker.bytes.Add(n) })
		if err == nil {
			cache.putFull(file, hash)
		}
		return hash, err
	})

	byHash := make(map[key][]File)
	for _, file := range candidates {
		if hash, ok := full[file.Path]; ok {
			k := key{file.Size, hash}
			byHash[k] = append(byHash[k], file)
		}
	}
	for k, same := range byHash {
		if len(same) < 2 {
			continue
		}
		slices.SortFunc(same, func(a, b File) int {
			switch {
			case a.Path < b.Path:
				return -1
			case a.Path > b.Path:
				return 1
			}
			return 0
		})
		allocated := same[0].Allocated
		for _, file := range same {
			allocated = min(allocated, file.Allocated)
		}
		result.Groups = append(result.Groups, Group{Hash: k.hash, Size: k.size, Allocated: allocated, Files: same})
	}
	slices.SortFunc(result.Groups, func(a, b Group) int {
		wasteA, wasteB := a.Allocated*int64(len(a.Files)-1), b.Allocated*int64(len(b.Files)-1)
		switch {
		case wasteA > wasteB:
			return -1
		case wasteA < wasteB:
			return 1
		case a.Hash < b.Hash:
			return -1
		case a.Hash > b.Hash:
			return 1
		}
		return 0
	})

	result.Canceled = ctx.Err() != nil
	result.Unreadable = unreadable.Load()
	cache.flush()
	return result
}

func bySizeDesc(a, b File) int {
	switch {
	case a.Size > b.Size:
		return -1
	case a.Size < b.Size:
		return 1
	case a.Path < b.Path:
		return -1
	case a.Path > b.Path:
		return 1
	}
	return 0
}

// hashAll calcula un hash por archivo con varios lectores. Un archivo ilegible se
// cuenta y se omite: nunca detiene la búsqueda.
func hashAll(ctx context.Context, files []File, workers int, tracker *Tracker, unreadable *atomic.Int64, hash func(context.Context, File) (string, error)) map[string]string {
	result := make(map[string]string, len(files))
	var mu sync.Mutex
	queue := make(chan File)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for file := range queue {
				path := file.Path
				tracker.current.Store(&path)
				value, err := hash(ctx, file)
				tracker.done.Add(1)
				switch {
				case err == nil:
					mu.Lock()
					result[file.Path] = value
					mu.Unlock()
				case !errors.Is(err, context.Canceled):
					unreadable.Add(1)
				}
			}
		}()
	}
loop:
	for _, file := range files {
		select {
		case queue <- file:
		case <-ctx.Done():
			break loop
		}
	}
	close(queue)
	wg.Wait()
	return result
}
