package duplicates

import (
	"database/sql"
	"os"
	"sync"

	_ "modernc.org/sqlite"
)

const (
	cacheFlushEvery = 200
	cacheSchema     = `CREATE TABLE IF NOT EXISTS hashes (
		path TEXT PRIMARY KEY,
		size INTEGER NOT NULL,
		modified INTEGER NOT NULL,
		partial TEXT,
		full TEXT
	)`
	// Si cambia el tamaño o la fecha, lo guardado de antes ya no vale.
	cacheUpsert = `INSERT INTO hashes (path, size, modified, partial, full) VALUES (?1, ?2, ?3, ?4, ?5)
		ON CONFLICT(path) DO UPDATE SET
			partial = CASE WHEN size = excluded.size AND modified = excluded.modified
				THEN COALESCE(excluded.partial, partial) ELSE excluded.partial END,
			full = CASE WHEN size = excluded.size AND modified = excluded.modified
				THEN COALESCE(excluded.full, full) ELSE excluded.full END,
			size = excluded.size,
			modified = excluded.modified`
)

// Cache recuerda hashes por ruta + tamaño + fecha, para no releer lo que no cambió y
// poder continuar una búsqueda cancelada. Un Cache nil funciona como caché vacía:
// perderla solo hace la búsqueda más lenta, nunca debe impedirla.
type Cache struct {
	db *sql.DB

	mu      sync.Mutex
	pending []cacheRow
}

type cacheRow struct {
	file          File
	partial, full sql.NullString
}

// OpenCache abre la caché. Si el archivo está dañado lo recrea, y si aun así falla
// devuelve nil.
func OpenCache(path string) *Cache {
	if cache := openCache(path); cache != nil {
		return cache
	}
	for _, suffix := range []string{"", "-journal", "-wal", "-shm"} {
		os.Remove(path + suffix)
	}
	return openCache(path)
}

func openCache(path string) *Cache {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil
	}
	db.SetMaxOpenConns(1)
	// Crear la tabla no siempre lee el archivo completo: la consulta revela si está
	// dañado.
	if _, err := db.Exec(cacheSchema); err != nil {
		db.Close()
		return nil
	}
	var check string
	if err := db.QueryRow(`PRAGMA quick_check(1)`).Scan(&check); err != nil || check != "ok" {
		db.Close()
		return nil
	}
	return &Cache{db: db}
}

func (c *Cache) read(column string, file File) (string, bool) {
	if c == nil {
		return "", false
	}
	var value sql.NullString
	err := c.db.QueryRow(`SELECT `+column+` FROM hashes WHERE path = ? AND size = ? AND modified = ?`,
		file.Path, file.Size, file.Modified).Scan(&value)
	if err != nil || !value.Valid {
		return "", false
	}
	return value.String, true
}

func (c *Cache) partial(file File) (string, bool) { return c.read("partial", file) }
func (c *Cache) full(file File) (string, bool)    { return c.read("full", file) }

func (c *Cache) putPartial(file File, hash string) {
	c.put(cacheRow{file: file, partial: sql.NullString{String: hash, Valid: true}})
}

func (c *Cache) putFull(file File, hash string) {
	c.put(cacheRow{file: file, full: sql.NullString{String: hash, Valid: true}})
}

func (c *Cache) put(row cacheRow) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.pending = append(c.pending, row)
	full := len(c.pending) >= cacheFlushEvery
	c.mu.Unlock()
	if full {
		c.flush()
	}
}

// flush escribe lo pendiente. Un hash completo tarda en calcularse, así que se guarda
// en tandas pero sin esperar al final: una cancelación no lo pierde.
func (c *Cache) flush() {
	if c == nil {
		return
	}
	c.mu.Lock()
	rows := c.pending
	c.pending = nil
	c.mu.Unlock()
	if len(rows) == 0 {
		return
	}

	tx, err := c.db.Begin()
	if err != nil {
		return
	}
	for _, row := range rows {
		if _, err := tx.Exec(cacheUpsert, row.file.Path, row.file.Size, row.file.Modified, row.partial, row.full); err != nil {
			tx.Rollback()
			return
		}
	}
	tx.Commit()
}

// Close escribe lo pendiente y cierra.
func (c *Cache) Close() {
	if c == nil {
		return
	}
	c.flush()
	c.db.Close()
}
