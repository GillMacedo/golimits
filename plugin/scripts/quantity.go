package main

import (
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
)

// Kubernetes resource quantities, as in k8s.io/apimachinery/pkg/api/resource.

var quantityRE = regexp.MustCompile(`^([+-]?)([0-9]+\.?[0-9]*|\.[0-9]+)([eE][+-]?[0-9]+|[KMGTPE]i|[numkMGTPE]?)$`)

// parseQuantity returns the exact value of a Kubernetes quantity.
func parseQuantity(s string) (*big.Rat, error) {
	m := quantityRE.FindStringSubmatch(s)
	if m == nil {
		return nil, fmt.Errorf("%q is not a Kubernetes quantity", s)
	}
	num, ok := new(big.Rat).SetString(m[2])
	if !ok {
		return nil, fmt.Errorf("%q is not a Kubernetes quantity", s)
	}
	if m[1] == "-" {
		num.Neg(num)
	}
	suf := m[3]
	mult := new(big.Rat).SetInt64(1)
	pow := func(base, exp int64) *big.Rat {
		return new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(base), big.NewInt(exp), nil))
	}
	switch {
	case suf == "":
	case len(suf) == 2 && suf[1] == 'i':
		mult = pow(1024, int64(1+indexByte("KMGTPE", suf[0])))
	case suf[0] == 'e' || suf[0] == 'E':
		e, err := strconv.ParseInt(suf[1:], 10, 32)
		if err != nil || e > 30 || e < -30 {
			return nil, fmt.Errorf("%q has an exponent out of range", s)
		}
		if e >= 0 {
			mult = pow(10, e)
		} else {
			mult = new(big.Rat).Inv(pow(10, -e))
		}
	default:
		switch suf {
		case "n":
			mult = new(big.Rat).Inv(pow(10, 9))
		case "u":
			mult = new(big.Rat).Inv(pow(10, 6))
		case "m":
			mult = new(big.Rat).Inv(pow(10, 3))
		case "k":
			mult = pow(10, 3)
		default:
			mult = pow(10, int64(3*(2+indexByte("MGTPE", suf[0]))))
		}
	}
	return num.Mul(num, mult), nil
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// ceilInt rounds r up to an int64, saturating.
func ceilInt(r *big.Rat) int64 {
	q, rem := new(big.Int).QuoRem(r.Num(), r.Denom(), new(big.Int))
	if rem.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	}
	if !q.IsInt64() {
		if q.Sign() > 0 {
			return math.MaxInt64
		}
		return math.MinInt64
	}
	return q.Int64()
}

// downwardValue is what the kubelet writes for a resourceFieldRef:
// ceil(value/divisor), with CPU compared in millicores.
func downwardValue(value, divisor *big.Rat, cpu bool) int64 {
	if cpu {
		thousand := new(big.Rat).SetInt64(1000)
		v := ceilInt(new(big.Rat).Mul(value, thousand))
		d := ceilInt(new(big.Rat).Mul(divisor, thousand))
		if d <= 0 {
			return 0
		}
		return ceilInt(new(big.Rat).SetFrac(big.NewInt(v), big.NewInt(d)))
	}
	v := ceilInt(value)
	d := ceilInt(divisor)
	if d <= 0 {
		return 0
	}
	return ceilInt(new(big.Rat).SetFrac(big.NewInt(v), big.NewInt(d)))
}

// parseByteCount is the Go runtime's parser for GOMEMLIMIT
// (runtime/string.go, unchanged since Go 1.19).
func parseByteCount(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	last := s[len(s)-1]
	if last >= '0' && last <= '9' {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n < 0 {
			return 0, false
		}
		return n, true
	}
	if last != 'B' || len(s) < 2 {
		return 0, false
	}
	if c := s[len(s)-2]; c >= '0' && c <= '9' {
		n, err := strconv.ParseInt(s[:len(s)-1], 10, 64)
		if err != nil || n < 0 {
			return 0, false
		}
		return n, true
	} else if c != 'i' {
		return 0, false
	}
	if len(s) < 4 {
		return 0, false
	}
	power := 0
	switch s[len(s)-3] {
	case 'K':
		power = 1
	case 'M':
		power = 2
	case 'G':
		power = 3
	case 'T':
		power = 4
	default:
		return 0, false
	}
	m := uint64(1)
	for i := 0; i < power; i++ {
		m *= 1024
	}
	n, err := strconv.ParseInt(s[:len(s)-3], 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	un := uint64(n)
	if un > math.MaxUint64/m {
		return 0, false
	}
	un *= m
	if un > uint64(math.MaxInt64) {
		return 0, false
	}
	return int64(un), true
}

// formatBytes prints n the way GOMEMLIMIT accepts it, as the largest exact
// binary unit.
func formatBytes(n int64) string {
	units := []string{"TiB", "GiB", "MiB", "KiB"}
	for i, u := range units {
		size := int64(1) << (10 * (4 - i))
		if n >= size && n%size == 0 {
			return fmt.Sprintf("%d%s", n/size, u)
		}
	}
	return fmt.Sprintf("%dB", n)
}

// humanBytes prints n rounded, for messages only.
func humanBytes(n int64) string {
	f := float64(n)
	switch {
	case f >= 1<<30:
		return trimFloat(f/(1<<30)) + " GiB"
	case f >= 1<<20:
		return trimFloat(f/(1<<20)) + " MiB"
	case f >= 1<<10:
		return trimFloat(f/(1<<10)) + " KiB"
	}
	return fmt.Sprintf("%d bytes", n)
}

func trimFloat(f float64) string {
	s := strconv.FormatFloat(f, 'f', 2, 64)
	for len(s) > 0 && s[len(s)-1] == '0' {
		s = s[:len(s)-1]
	}
	if len(s) > 0 && s[len(s)-1] == '.' {
		s = s[:len(s)-1]
	}
	return s
}
