package learning

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	fsrs "github.com/open-spaced-repetition/go-fsrs/v4"
)

// FSRSPolicyConfig holds FSRS scheduling parameters from policy JSON
type FSRSPolicyConfig struct {
	AlgorithmVersion      string       `json:"algorithm_version"`
	Implementation        string       `json:"implementation"`
	ImplementationVersion string       `json:"implementation_version"`
	DesiredRetention      float64      `json:"desired_retention"`
	MaximumIntervalDays   float64      `json:"maximum_interval_days"`
	EnableShortTerm       bool         `json:"enable_short_term"`
	EnableFuzz            bool         `json:"enable_fuzz"`
	ParameterSet          string       `json:"parameter_set"`
	Weights               fsrs.Weights `json:"weights"`
	LearningStepsMinutes  []float64    `json:"learning_steps_minutes"`
	RelearningStepsMin    []float64    `json:"relearning_steps_minutes"`
}

type FSRSScheduler struct {
	fsrs   *fsrs.FSRS
	params fsrs.Parameters
	policy FSRSPolicyConfig
}

func NewFSRSScheduler(cfg FSRSPolicyConfig) *FSRSScheduler {
	p := fsrs.DefaultParam()
	if cfg.Weights != (fsrs.Weights{}) {
		p.W = cfg.Weights
	}
	if cfg.DesiredRetention > 0 {
		p.RequestRetention = cfg.DesiredRetention
	}
	if cfg.MaximumIntervalDays > 0 {
		p.MaximumInterval = cfg.MaximumIntervalDays
	}
	p.EnableShortTerm = cfg.EnableShortTerm
	p.EnableFuzz = cfg.EnableFuzz
	if len(cfg.LearningStepsMinutes) > 0 {
		p.LearningSteps = cfg.LearningStepsMinutes
	}
	if len(cfg.RelearningStepsMin) > 0 {
		p.RelearningSteps = cfg.RelearningStepsMin
	}

	return &FSRSScheduler{
		fsrs:   fsrs.NewFSRS(p),
		params: p,
		policy: cfg,
	}
}

// LoadFSRSPolicyFromPath reads FSRS configuration from a policy JSON file
func LoadFSRSPolicyFromPath(path string) (*FSRSPolicyConfig, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read policy file %q: %w", path, err)
	}

	var root struct {
		FSRS FSRSPolicyConfig `json:"fsrs"`
	}
	if err := json.Unmarshal(bytes, &root); err != nil {
		return nil, fmt.Errorf("unmarshal policy JSON: %w", err)
	}

	return &root.FSRS, nil
}

// TimeToMicro converts a time.Time to Unix microseconds UTC
func TimeToMicro(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UTC().UnixNano() / 1000
}

// MicroToTime converts a Unix microsecond timestamp to UTC time.Time
func MicroToTime(us int64) time.Time {
	if us == 0 {
		return time.Time{}
	}
	sec := us / 1000000
	nsec := (us % 1000000) * 1000
	return time.Unix(sec, nsec).UTC()
}

// CardFromMemoryProjection rebuilds an fsrs.Card from a MemoryProjection
func CardFromMemoryProjection(mem *MemoryProjection) fsrs.Card {
	if mem == nil || mem.FSRSState == 0 {
		return fsrs.NewCard()
	}

	var lastReview time.Time
	if mem.LastReviewAt != nil && *mem.LastReviewAt > 0 {
		lastReview = MicroToTime(*mem.LastReviewAt)
	}

	due := MicroToTime(mem.DueAt)
	return fsrs.Card{
		Due:            due,
		Stability:      mem.Stability,
		Difficulty:     mem.Difficulty,
		ScheduledDays:  uint64(mem.ScheduledDays),
		Reps:           uint64(mem.Reps),
		Lapses:         uint64(mem.Lapses),
		State:          fsrs.State(mem.FSRSState),
		LastReview:     lastReview,
		RemainingSteps: mem.FSRSStep,
	}
}

// StepReview applies a review (Good or Again) using explicitly provided review time
func (s *FSRSScheduler) StepReview(card fsrs.Card, reviewTime time.Time, isCorrect bool) (nextCard fsrs.Card, elapsedDays float64, scheduledDays float64, rating int, err error) {
	r := fsrs.Again
	rating = 1
	if isCorrect {
		r = fsrs.Good
		rating = 3
	}

	if card.State == fsrs.State(0) && card.Due.IsZero() {
		// New card initialization
		card = fsrs.NewCard(reviewTime)
		elapsedDays = 0
	} else if !card.LastReview.IsZero() {
		diff := reviewTime.Sub(card.LastReview)
		if diff < 0 {
			diff = 0
		}
		elapsedDays = diff.Seconds() / 86400.0
	}

	info, err := s.fsrs.Next(card, reviewTime, r)
	if err != nil {
		return card, 0, 0, rating, fmt.Errorf("fsrs step next: %w", err)
	}

	nextCard = info.Card
	scheduledDays = float64(nextCard.ScheduledDays)
	return nextCard, elapsedDays, scheduledDays, rating, nil
}
