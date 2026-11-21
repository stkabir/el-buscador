package main

import "el-buscador/internal/disk"

// DiskService expone al frontend la información del disco.
type DiskService struct{}

// Drives devuelve los discos que se pueden revisar, con el del sistema primero.
func (*DiskService) Drives() ([]disk.Usage, error) {
	return disk.Fixed()
}
