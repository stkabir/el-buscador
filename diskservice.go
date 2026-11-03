package main

import "el-buscador/internal/disk"

// DiskService expone al frontend la información del disco.
type DiskService struct{}

// Usage devuelve el espacio total y libre del disco del sistema.
func (*DiskService) Usage() (disk.Usage, error) {
	return disk.System()
}
