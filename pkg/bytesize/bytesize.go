package bytesize

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type ByteSize int64

type Unit struct {
	suffixes   []string
	multiplier int64
}

var (
	// Base-10 Byte Units

	// Byte is the base unit of digital information (8 bits).
	// Suffix can be excluded or one of the following: B, Bi (case insensitive).
	Byte = Unit{
		suffixes:   []string{"B", "Bi", ""},
		multiplier: 1,
	}
	// Kilobyte is a unit of digital information in base-10.
	// Suffix can be one of the following: KB, k (case insensitive).
	Kilobyte = Unit{
		suffixes:   []string{"KB", "K"},
		multiplier: 1e3,
	}
	// Megabyte is a unit of digital information in base-10.
	// Suffix can be one of the following: MB, M (case insensitive).
	Megabyte = Unit{
		suffixes:   []string{"MB", "M"},
		multiplier: 1e6,
	}
	// Gigabyte is a unit of digital information in base-10.
	// Suffix can be one of the following: GB, G (case insensitive).
	Gigabyte = Unit{
		suffixes:   []string{"GB", "G"},
		multiplier: 1e9,
	}
	// Terabyte is a unit of digital information in base-10.
	// Suffix can be one of the following: TB, T (case insensitive).
	Terabyte = Unit{
		suffixes:   []string{"TB", "T"},
		multiplier: 1e12,
	}
	// Petabyte is a unit of digital information in base-10.
	// Suffix can be one of the following: PB, P (case insensitive).
	Petabyte = Unit{
		suffixes:   []string{"PB", "P"},
		multiplier: 1e15,
	}
	// Exabyte is a unit of digital information in base-10.
	// Suffix can be one of the following: EB, E (case insensitive).
	Exabyte = Unit{
		suffixes:   []string{"EB", "E"},
		multiplier: 1e18,
	}

	// Base-2 Byte Units

	// Kibibyte is a unit of digital information in base-2.
	// Suffix can be one of the following: KiB, Ki (case insensitive).
	Kibibyte = Unit{
		suffixes:   []string{"KiB", "Ki"},
		multiplier: 1 << 10,
	}
	// Mebibyte is a unit of digital information in base-2.
	// Suffix can be one of the following: MiB, Mi (case insensitive).
	Mebibyte = Unit{
		suffixes:   []string{"MiB", "Mi"},
		multiplier: 1 << 20,
	}
	// Gibibyte is a unit of digital information in base-2.
	// Suffix can be one of the following: GiB, Gi (case insensitive).
	Gibibyte = Unit{
		suffixes:   []string{"GiB", "Gi"},
		multiplier: 1 << 30,
	}
	// Tebibyte is a unit of digital information in base-2.
	// Suffix can be one of the following: TiB, Ti (case insensitive).
	Tebibyte = Unit{
		suffixes:   []string{"TiB", "Ti"},
		multiplier: 1 << 40,
	}
	// Pebibyte is a unit of digital information in base-2.
	// Suffix can be one of the following: PiB, Pi (case insensitive).
	Pebibyte = Unit{
		suffixes:   []string{"PiB", "Pi"},
		multiplier: 1 << 50,
	}
	// Exbibyte is a unit of digital information in base-2.
	// Suffix can be one of the following: EiB, Ei (case insensitive).
	Exbibyte = Unit{
		suffixes:   []string{"EiB", "Ei"},
		multiplier: 1 << 60,
	}

	// units is a list of all possible byte units.
	// The list is sorted from the largest to the smallest unit.
	units = []Unit{
		Exbibyte, Exabyte,
		Pebibyte, Petabyte,
		Tebibyte, Terabyte,
		Gibibyte, Gigabyte,
		Mebibyte, Megabyte,
		Kibibyte, Kilobyte,
		Byte,
	}

	// sizeRegex matches a string containing a number followed by an optional unit.
	//
	// Examples:
	//   2    - 2 bytes
	//   2k   - 2 kilobytes
	//   2Ki  - 2 kibibytes
	//   2.5M - 2.5 megabytes
	sizeRegex = regexp.MustCompile(`^([0-9\.]+)\s*([a-zA-Z]*)$`)
)

// Parse parses a string into a ByteSize value.
// The string should contain a number followed by an optional unit.
// The unit is case-insensitive.
// If the string is invalid, an error is returned.
//
// Examples:
//
//	2    - 2 bytes
//	2k   - 2 kilobytes
//	2Ki  - 2 kibibytes
//	2.5M - 2.5 megabytes
//	2.5  - 2.5 bytes
func Parse(s string) (ByteSize, error) {
	matches := sizeRegex.FindStringSubmatch(s)
	if matches == nil {
		return 0, fmt.Errorf("invalid bytesize: %v", s)
	}
	value, err := strconv.ParseFloat(matches[1], 64)
	if err != nil {
		return 0, fmt.Errorf("invalid bytesize: %v, %w", s, err)
	}
	unit := matches[2]
	realUnit, err := FindUnit(unit)
	if err != nil {
		return 0, err
	}

	return ByteSize(value * float64(realUnit.multiplier)), nil
}

// FindUnit returns the Unit type that corresponds to the given unit string.
// The unit string is case-insensitive.
// If the unit string is invalid, an error is returned.
func FindUnit(unit string) (Unit, error) {
	for _, u := range units {
		for _, suffix := range u.suffixes {
			if strings.EqualFold(unit, suffix) {
				return u, nil
			}
		}
	}
	return Unit{}, fmt.Errorf("invalid unit: %v", unit)
}

// String returns a string representation of the ByteSize value in the largest possible unit.
func (b ByteSize) String() string {
	// Find the largest unit that is smaller than the value.
	for _, u := range units {
		if float64(b) >= float64(u.multiplier) {
			value := float64(b) / float64(u.multiplier)
			return fmt.Sprintf("%v %v", value, u.suffixes[0])
		}
	}
	return "0 B"
}

// As takes a Unit type and returns the ByteSize value in that unit.
func (b ByteSize) As(unit Unit) float64 {
	realUnit, err := FindUnit(unit.suffixes[0])
	if err != nil {
		panic("invariant violated: invalid unit")
	}
	return float64(b) / float64(realUnit.multiplier)
}

// Bytes returns the ByteSize value in bytes.
func (b ByteSize) Bytes() float64 {
	return b.As(Byte)
}

// Kilobytes returns the ByteSize value in kilobytes (base-10).
func (b ByteSize) Kilobytes() float64 {
	return b.As(Kilobyte)
}

// Kibibytes returns the ByteSize value in kibibytes (base-2).
func (b ByteSize) Kibibytes() float64 {
	return b.As(Kibibyte)
}

// Megabytes returns the ByteSize value in megabytes (base-10).
func (b ByteSize) Megabytes() float64 {
	return b.As(Megabyte)
}

// Mebibytes returns the ByteSize value in mebibytes (base-2).
func (b ByteSize) Mebibytes() float64 {
	return b.As(Mebibyte)
}

// Gigabytes returns the ByteSize value in gigabytes (base-10).
func (b ByteSize) Gigabytes() float64 {
	return b.As(Gigabyte)
}

// Gibibytes returns the ByteSize value in gibibytes (base-2).
func (b ByteSize) Gibibytes() float64 {
	return b.As(Gibibyte)
}

// Terabytes returns the ByteSize value in terabytes (base-10).
func (b ByteSize) Terabytes() float64 {
	return b.As(Terabyte)
}

// Tebibytes returns the ByteSize value in tebibytes (base-2).
func (b ByteSize) Tebibytes() float64 {
	return b.As(Tebibyte)
}

// Petabytes returns the ByteSize value in petabytes (base-10).
func (b ByteSize) Petabytes() float64 {
	return b.As(Petabyte)
}

// Pebibytes returns the ByteSize value in pebibytes (base-2).
func (b ByteSize) Pebibytes() float64 {
	return b.As(Pebibyte)
}

// Exabytes returns the ByteSize value in exabytes (base-10).
func (b ByteSize) Exabytes() float64 {
	return b.As(Exabyte)
}

// Exbibytes returns the ByteSize value in exbibytes (base-2).
func (b ByteSize) Exbibytes() float64 {
	return b.As(Exbibyte)
}
