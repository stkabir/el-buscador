// Package scanner recorre un disco y calcula qué ocupa espacio, sin abrir ni
// modificar archivos.
package scanner

import (
	"container/heap"
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	topFilesLimit = 100
	topDirsLimit  = 30
	dirFilesLimit = 20
	// Una carpeta casi igual a su mayor subcarpeta no aporta nada a la lista.
	passThroughRatio = 0.9
	unreadableSample = 20
)

// Entry es un archivo o carpeta. Size es lo que ocupa en disco y Cloud lo que
// solo está en la nube.
type Entry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Size  int64  `json:"size"`
	Cloud int64  `json:"cloud"`
	Files int64  `json:"files"`
	IsDir bool   `json:"isDir"`
	// Unreadable indica una carpeta que no se pudo abrir: su tamaño es desconocido.
	Unreadable bool `json:"unreadable"`
}

// Summary resume un escaneo terminado.
type Summary struct {
	Root       string   `json:"root"`
	Size       int64    `json:"size"`
	Cloud      int64    `json:"cloud"`
	Files      int64    `json:"files"`
	Dirs       int64    `json:"dirs"`
	HardLinks  int64    `json:"hardLinks"`
	Links      int64    `json:"links"`
	Unreadable int64    `json:"unreadable"`
	Samples    []string `json:"samples"`
	Elevated   bool     `json:"elevated"`
	DurationMs int64    `json:"durationMs"`
	TopFiles   []Entry  `json:"topFiles"`
	TopDirs    []Entry  `json:"topDirs"`
}

// Listing es el contenido de una carpeta. Solo guarda sus archivos más grandes;
// el resto se resume en OtherFiles y OtherSize.
type Listing struct {
	Entry
	Parent     string  `json:"parent"`
	Dirs       []Entry `json:"dirs"`
	FileItems  []Entry `json:"fileItems"`
	OtherFiles int64   `json:"otherFiles"`
	OtherSize  int64   `json:"otherSize"`
}

// Progress es una foto del avance de un escaneo.
type Progress struct {
	Files   int64  `json:"files"`
	Dirs    int64  `json:"dirs"`
	Size    int64  `json:"size"`
	Current string `json:"current"`
}

// Tracker expone el avance de un escaneo en curso.
type Tracker struct {
	files, dirs, size atomic.Int64
	current           atomic.Pointer[string]
}

// Snapshot devuelve el avance actual.
func (t *Tracker) Snapshot() Progress {
	p := Progress{Files: t.files.Load(), Dirs: t.dirs.Load(), Size: t.size.Load()}
	if current := t.current.Load(); current != nil {
		p.Current = *current
	}
	return p
}

// Result guarda el árbol de un escaneo para poder navegarlo.
type Result struct {
	root    *node
	summary Summary
}

// Summary devuelve el resumen del escaneo.
func (r *Result) Summary() Summary { return r.summary }

// Listing devuelve el contenido de la carpeta indicada.
func (r *Result) Listing(path string) (Listing, error) {
	n := r.root.find(path)
	if n == nil {
		return Listing{}, fmt.Errorf("carpeta no encontrada: %s", path)
	}

	listing := Listing{
		Entry:      n.entry(),
		Dirs:       make([]Entry, 0, len(n.children)),
		FileItems:  make([]Entry, 0, len(n.files)),
		OtherFiles: n.otherFiles,
		OtherSize:  n.otherSize,
	}
	if n.parent != nil {
		listing.Parent = n.parent.path()
	}
	for _, child := range n.children {
		listing.Dirs = append(listing.Dirs, child.entry())
	}
	for _, file := range n.files {
		listing.FileItems = append(listing.FileItems, Entry{
			Name:  file.name,
			Path:  n.join(file.name),
			Size:  file.size,
			Cloud: file.cloud,
			Files: 1,
		})
	}
	return listing, nil
}

// WalkDirs recorre las carpetas bajo la raíz, sin incluirla. Si visit devuelve
// false, no entra en esa carpeta.
func (r *Result) WalkDirs(visit func(Entry) bool) {
	var walk func(*node)
	walk = func(n *node) {
		for _, child := range n.children {
			if visit(child.entry()) {
				walk(child)
			}
		}
	}
	walk(r.root)
}

// Lookup devuelve la carpeta indicada, si el escaneo la encontró.
func (r *Result) Lookup(path string) (Entry, bool) {
	n := r.root.find(path)
	if n == nil {
		return Entry{}, false
	}
	return n.entry(), true
}

// LastActivity devuelve la última modificación dentro de una carpeta, en segundos
// Unix, sin contar las subcarpetas directas que exclude descarta. Devuelve 0 si no
// la conoce.
func (r *Result) LastActivity(path string, exclude func(name string) bool) int64 {
	n := r.root.find(path)
	if n == nil {
		return 0
	}
	latest := n.ownLatest
	for _, child := range n.children {
		if !exclude(child.name) {
			latest = max(latest, child.totalLatest)
		}
	}
	return latest
}

type fileEntry struct {
	name        string
	size, cloud int64
}

type node struct {
	name       string
	parent     *node
	children   []*node
	files      []fileEntry
	otherFiles int64
	otherSize  int64
	ownFiles   int64
	ownSize    int64
	ownCloud   int64
	totalFiles int64
	totalSize  int64
	totalCloud int64
	// ownLatest y totalLatest son la última modificación de un archivo, en
	// segundos Unix.
	ownLatest   int64
	totalLatest int64
	unreadable  bool
}

func (n *node) path() string {
	if n.parent == nil {
		return n.name
	}
	return n.parent.join(n.name)
}

func (n *node) join(name string) string {
	path := n.path()
	if strings.HasSuffix(path, `\`) {
		return path + name
	}
	return path + `\` + name
}

func (n *node) entry() Entry {
	return Entry{
		Name:  n.name,
		Path:  n.path(),
		Size:  n.totalSize,
		Cloud: n.totalCloud,
		Files: n.totalFiles,
		IsDir: true,

		Unreadable: n.unreadable,
	}
}

func (n *node) find(path string) *node {
	root := n.path()
	if len(path) < len(root) || !strings.EqualFold(path[:len(root)], root) {
		return nil
	}

	current := n
	for _, part := range strings.Split(strings.Trim(path[len(root):], `\`), `\`) {
		if part == "" {
			continue
		}
		var next *node
		for _, child := range current.children {
			if strings.EqualFold(child.name, part) {
				next = child
				break
			}
		}
		if next == nil {
			return nil
		}
		current = next
	}
	return current
}

// sum calcula los totales de abajo hacia arriba y ordena los hijos por tamaño.
func (n *node) sum() {
	n.totalFiles, n.totalSize, n.totalCloud, n.totalLatest = n.ownFiles, n.ownSize, n.ownCloud, n.ownLatest
	for _, child := range n.children {
		child.sum()
		n.totalFiles += child.totalFiles
		n.totalSize += child.totalSize
		n.totalCloud += child.totalCloud
		n.totalLatest = max(n.totalLatest, child.totalLatest)
	}
	slices.SortFunc(n.children, func(a, b *node) int { return compareDesc(a.totalSize, b.totalSize) })
}

func (n *node) collectTopDirs(out *[]*node) {
	for _, child := range n.children {
		isPassThrough := len(child.children) > 0 &&
			float64(child.children[0].totalSize) >= float64(child.totalSize)*passThroughRatio
		if child.totalSize > 0 && !isPassThrough {
			*out = append(*out, child)
		}
		child.collectTopDirs(out)
	}
}

func compareDesc(a, b int64) int {
	switch {
	case a > b:
		return -1
	case a < b:
		return 1
	}
	return 0
}

type walker struct {
	ctx     context.Context
	tracker *Tracker
	sem     chan struct{}
	wg      sync.WaitGroup

	hardLinks, links, unreadable atomic.Int64

	seenMu sync.Mutex
	seen   map[int64]struct{}

	topMu    sync.Mutex
	top      fileHeap
	topFloor atomic.Int64

	samplesMu sync.Mutex
	samples   []string
}

// Scan recorre root y devuelve su árbol. Devuelve el error del contexto si se
// cancela. tracker recibe el avance y puede ser nil.
func Scan(ctx context.Context, root string, tracker *Tracker) (*Result, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("ruta %q: %w", root, err)
	}
	if tracker == nil {
		tracker = &Tracker{}
	}

	started := time.Now()
	w := &walker{
		ctx:     ctx,
		tracker: tracker,
		sem:     make(chan struct{}, max(4, min(runtime.NumCPU(), 16))),
		seen:    make(map[int64]struct{}),
	}
	rootNode := &node{name: abs}
	w.visit(rootNode)
	w.wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	rootNode.sum()

	var dirs []*node
	rootNode.collectTopDirs(&dirs)
	slices.SortFunc(dirs, func(a, b *node) int { return compareDesc(a.totalSize, b.totalSize) })
	dirs = dirs[:min(len(dirs), topDirsLimit)]
	topDirs := make([]Entry, 0, len(dirs))
	for _, dir := range dirs {
		topDirs = append(topDirs, dir.entry())
	}

	topFiles := make([]Entry, len(w.top))
	copy(topFiles, w.top)
	slices.SortFunc(topFiles, func(a, b Entry) int { return compareDesc(a.Size, b.Size) })

	return &Result{
		root: rootNode,
		summary: Summary{
			Root:       abs,
			Size:       rootNode.totalSize,
			Cloud:      rootNode.totalCloud,
			Files:      rootNode.totalFiles,
			Dirs:       tracker.dirs.Load(),
			HardLinks:  w.hardLinks.Load(),
			Links:      w.links.Load(),
			Unreadable: w.unreadable.Load(),
			Samples:    w.samples,
			Elevated:   isElevated(),
			DurationMs: time.Since(started).Milliseconds(),
			TopFiles:   topFiles,
			TopDirs:    topDirs,
		},
	}, nil
}

// visit lee una carpeta y reparte sus subcarpetas: en una goroutine nueva si hay
// cupo, o en la actual si no, para no esperar un cupo que nunca se libere.
func (w *walker) visit(n *node) {
	if w.ctx.Err() != nil {
		return
	}
	w.read(n)
	for _, child := range n.children {
		select {
		case w.sem <- struct{}{}:
			w.wg.Add(1)
			go func() {
				defer w.wg.Done()
				defer func() { <-w.sem }()
				w.visit(child)
			}()
		default:
			w.visit(child)
		}
	}
}

func (w *walker) read(n *node) {
	path := n.path()
	w.tracker.current.Store(&path)
	w.tracker.dirs.Add(1)

	var files []fileEntry
	err := readDir(path, func(e rawEntry) {
		switch classify(e.attrs, e.tag) {
		case kindDir:
			n.children = append(n.children, &node{name: e.name, parent: n})
		case kindLink:
			w.links.Add(1)
		case kindFile:
			n.ownLatest = max(n.ownLatest, e.lastWrite)
			if w.isRepeated(e.id) {
				w.hardLinks.Add(1)
				return
			}
			size, cloud := measure(e.attrs, e.endOfFile, e.allocated)
			files = append(files, fileEntry{name: e.name, size: size, cloud: cloud})
			n.ownFiles++
			n.ownSize += size
			n.ownCloud += cloud
			w.tracker.files.Add(1)
			w.tracker.size.Add(size)
			w.offerTop(n, e.name, size, cloud)
		}
	})
	if err != nil && !isGone(err) {
		n.unreadable = true
		w.unreadable.Add(1)
		w.samplesMu.Lock()
		if len(w.samples) < unreadableSample {
			w.samples = append(w.samples, path)
		}
		w.samplesMu.Unlock()
	}

	slices.SortFunc(files, func(a, b fileEntry) int { return compareDesc(a.size+a.cloud, b.size+b.cloud) })
	if len(files) > dirFilesLimit {
		for _, file := range files[dirFilesLimit:] {
			n.otherFiles++
			n.otherSize += file.size
		}
		files = slices.Clone(files[:dirFilesLimit])
	}
	n.files = files
}

// isRepeated indica si el ID ya se vio: es otro hard link del mismo archivo. Un
// ID 0 o -1 significa que el sistema de archivos no da uno confiable.
func (w *walker) isRepeated(id int64) bool {
	if id == 0 || id == -1 {
		return false
	}
	w.seenMu.Lock()
	defer w.seenMu.Unlock()
	if _, ok := w.seen[id]; ok {
		return true
	}
	w.seen[id] = struct{}{}
	return false
}

func (w *walker) offerTop(n *node, name string, size, cloud int64) {
	if size == 0 || size <= w.topFloor.Load() {
		return
	}
	entry := Entry{Name: name, Path: n.join(name), Size: size, Cloud: cloud, Files: 1}

	w.topMu.Lock()
	defer w.topMu.Unlock()
	heap.Push(&w.top, entry)
	if len(w.top) > topFilesLimit {
		heap.Pop(&w.top)
	}
	if len(w.top) == topFilesLimit {
		w.topFloor.Store(w.top[0].Size)
	}
}

// fileHeap es un montículo de mínimos: la raíz es el archivo que sale primero.
type fileHeap []Entry

func (h fileHeap) Len() int           { return len(h) }
func (h fileHeap) Less(i, j int) bool { return h[i].Size < h[j].Size }
func (h fileHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *fileHeap) Push(x any)        { *h = append(*h, x.(Entry)) }
func (h *fileHeap) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}
