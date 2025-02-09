package vmattrs

import "github.com/bwagner5/vm/pkg/bytesize"

type VMAttributes struct {
	CPUArchitectures []string
	CPUManufacturers []string
	Generations      *IntRange
	NetworkStorage   bytesize.ByteSize
}

type IntRange struct {
	Min int
	Max int
}

func NewBuilder() *VMAttributes {
	return &VMAttributes{}
}
