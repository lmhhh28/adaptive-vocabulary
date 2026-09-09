package core

import "github.com/google/uuid"

// UUIDGenerator abstracts UUID generation.
type UUIDGenerator interface {
	NewUUID() string
}

type RandomUUIDGenerator struct{}

func NewUUIDGenerator() UUIDGenerator {
	return &RandomUUIDGenerator{}
}

func (g *RandomUUIDGenerator) NewUUID() string {
	return uuid.NewString()
}

// NewUUID generates a new random UUID string using the default random generator.
func NewUUID() string {
	return uuid.NewString()
}
