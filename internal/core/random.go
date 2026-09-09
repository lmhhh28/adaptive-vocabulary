package core

import (
	crand "crypto/rand"
	"encoding/binary"
	randv2 "math/rand/v2"
)

// RandSource abstracts pseudo-random generation using standard library PCG.
type RandSource interface {
	Uint64() uint64
	IntN(n int) int
	Shuffle(n int, swap func(i, j int))
}

type PCGRand struct {
	pcg *randv2.PCG
	rng *randv2.Rand
}

func NewPCGRand(seed1, seed2 uint64) *PCGRand {
	p := randv2.NewPCG(seed1, seed2)
	return &PCGRand{
		pcg: p,
		rng: randv2.New(p),
	}
}

func NewCryptoSeededPCG() (*PCGRand, uint64, uint64, error) {
	var buf [16]byte
	if _, err := crand.Read(buf[:]); err != nil {
		return nil, 0, 0, err
	}
	s1 := binary.LittleEndian.Uint64(buf[0:8])
	s2 := binary.LittleEndian.Uint64(buf[8:16])
	return NewPCGRand(s1, s2), s1, s2, nil
}

func (r *PCGRand) Uint64() uint64 {
	return r.rng.Uint64()
}

func (r *PCGRand) IntN(n int) int {
	return r.rng.IntN(n)
}

func (r *PCGRand) Shuffle(n int, swap func(i, j int)) {
	r.rng.Shuffle(n, swap)
}
