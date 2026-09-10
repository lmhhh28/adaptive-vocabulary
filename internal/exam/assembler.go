package exam

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"adaptive-vocabulary/internal/content"
	"adaptive-vocabulary/internal/core"
)

var (
	ErrConstraintUnsatisfied = errors.New("exam constraint unsatisfied: no feasible assembly found")
	ErrAssemblyLimitReached  = errors.New("assembly search limit reached")
	ErrInvalidQuotas         = errors.New("invalid task quotas: does not match question count or invalid family")
)

var allowedTaskFamilies = map[string]bool{
	"meaning_recognition": true,
	"reverse_selection":   true,
	"context_selection":   true,
}

var englishWordRegex = regexp.MustCompile(`[a-zA-Z]+(?:['-][a-zA-Z]+)*`)

// CandidateQuestion represents a question loaded from DB for assembly consideration.
type CandidateQuestion struct {
	ID              string
	FormID          string
	SenseID         string
	SenseVersionID  string
	LexemeID        string
	TaskFamily      string
	Stem            string
	Options         []content.QuestionOption
	CorrectOptionID string
	Explanation     string
	// Precomputed for fast constraint checks:
	DistractorLexemeIDs []string
	StemLexemeIDs       []string
}

const (
	// DefaultMaxSearchSteps defines the default backtracking search step ceiling.
	DefaultMaxSearchSteps = 20000
)

// Assembler solves for a feasible paper satisfying all exam constraints.
type Assembler struct {
	db             *sql.DB
	maxSearchSteps int
}

func NewAssembler(db *sql.DB, maxSearchSteps int) *Assembler {
	if maxSearchSteps <= 0 {
		maxSearchSteps = DefaultMaxSearchSteps
	}
	return &Assembler{
		db:             db,
		maxSearchSteps: maxSearchSteps,
	}
}

// ResolveQuotas validates and computes the required quota for each task family.
func ResolveQuotas(questionCount int, requestedQuotas map[string]int) (map[string]int, error) {
	if questionCount <= 0 {
		return nil, errors.New("question count must be positive")
	}

	quotas := make(map[string]int)
	if len(requestedQuotas) == 0 {
		// Even split among the 3 families
		base := questionCount / 3
		rem := questionCount % 3
		order := []string{"meaning_recognition", "reverse_selection", "context_selection"}
		for i, fam := range order {
			q := base
			if i < rem {
				q++
			}
			quotas[fam] = q
		}
		return quotas, nil
	}

	// User provided quotas
	total := 0
	for fam, count := range requestedQuotas {
		if !allowedTaskFamilies[fam] {
			return nil, fmt.Errorf("%w: unknown task family %q", ErrInvalidQuotas, fam)
		}
		if count < 0 {
			return nil, fmt.Errorf("%w: negative quota for %q", ErrInvalidQuotas, fam)
		}
		quotas[fam] = count
		total += count
	}
	for fam := range allowedTaskFamilies {
		if _, ok := quotas[fam]; !ok {
			quotas[fam] = 0
		}
	}
	if total != questionCount {
		return nil, fmt.Errorf("%w: total quota (%d) != question count (%d)", ErrInvalidQuotas, total, questionCount)
	}

	return quotas, nil
}

// Assemble selects a paper of candidate questions satisfying all frozen constraints.
func (a *Assembler) Assemble(tc *core.TxnContext, bankID string, quotas map[string]int) ([]CandidateQuestion, error) {
	ctx := tc.Context()

	// 1. Build lexeme lookup maps: canonical_form and alias form -> lexeme_id
	formToLexeme := make(map[string]string)
	{
		rows, err := tc.Query("SELECT id, canonical_form FROM lexeme")
		if err != nil {
			return nil, fmt.Errorf("query lexemes: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id, cform string
			if err := rows.Scan(&id, &cform); err != nil {
				return nil, err
			}
			formToLexeme[strings.ToLower(strings.TrimSpace(cform))] = id
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("iterate lexemes: %w", err)
		}
	}
	{
		rows, err := tc.Query("SELECT form, lexeme_id FROM lexeme_alias")
		if err != nil {
			return nil, fmt.Errorf("query lexeme aliases: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var alias, lexemeID string
			if err := rows.Scan(&alias, &lexemeID); err != nil {
				return nil, err
			}
			formToLexeme[strings.ToLower(strings.TrimSpace(alias))] = lexemeID
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("iterate lexeme aliases: %w", err)
		}
	}

	// 2. Build sense_id -> lexeme_id map
	senseToLexeme := make(map[string]string)
	{
		rows, err := tc.Query("SELECT id, lexeme_id FROM sense")
		if err != nil {
			return nil, fmt.Errorf("query senses: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var sID, lID string
			if err := rows.Scan(&sID, &lID); err != nil {
				return nil, err
			}
			senseToLexeme[sID] = lID
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("iterate senses: %w", err)
		}
	}

	// 3. Load published questions belonging to the bank
	queryPool := `
		SELECT qv.id, qv.form_id, qv.sense_id, qv.sense_version_id, s.lexeme_id,
		       qv.task_family, qv.stem, qv.options_json, qv.correct_option_id, qv.explanation
		FROM question_version qv
		JOIN sense s ON qv.sense_id = s.id
		JOIN bank_sense bs ON bs.sense_id = qv.sense_id
		WHERE bs.bank_id = ? AND qv.status = 'published'
		ORDER BY qv.id ASC
	`
	rows, err := tc.Query(queryPool, bankID)
	if err != nil {
		return nil, fmt.Errorf("query question pool: %w", err)
	}
	defer rows.Close()

	byFamily := make(map[string][]CandidateQuestion)
	for rows.Next() {
		var q CandidateQuestion
		var optJSON string
		if err := rows.Scan(
			&q.ID, &q.FormID, &q.SenseID, &q.SenseVersionID, &q.LexemeID,
			&q.TaskFamily, &q.Stem, &optJSON, &q.CorrectOptionID, &q.Explanation,
		); err != nil {
			return nil, fmt.Errorf("scan candidate question: %w", err)
		}
		if err := json.Unmarshal([]byte(optJSON), &q.Options); err != nil {
			return nil, fmt.Errorf("unmarshal question options for %s: %w", q.ID, err)
		}

		// Precompute distractor lexeme IDs
		for _, opt := range q.Options {
			if opt.Language == "en" && opt.ID != q.CorrectOptionID {
				if lID, ok := senseToLexeme[opt.SenseID]; ok {
					q.DistractorLexemeIDs = append(q.DistractorLexemeIDs, lID)
				}
			}
		}

		// Precompute context stem lexeme IDs if context_selection
		if q.TaskFamily == "context_selection" {
			tokens := englishWordRegex.FindAllString(strings.ToLower(q.Stem), -1)
			for _, token := range tokens {
				if lID, ok := formToLexeme[token]; ok {
					q.StemLexemeIDs = append(q.StemLexemeIDs, lID)
				}
			}
		}

		byFamily[q.TaskFamily] = append(byFamily[q.TaskFamily], q)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate question pool: %w", err)
	}

	// Sort each family pool deterministically by ID
	for fam := range byFamily {
		sort.Slice(byFamily[fam], func(i, j int) bool {
			return byFamily[fam][i].ID < byFamily[fam][j].ID
		})
	}

	// 4. Backtracking search to solve quotas and constraints
	var requiredTasks []string
	for fam, count := range quotas {
		for i := 0; i < count; i++ {
			requiredTasks = append(requiredTasks, fam)
		}
	}
	// Sort task order deterministically
	sort.Strings(requiredTasks)

	steps := 0
	var chosen []CandidateQuestion

	// checkConstraints checks whether the candidate question conflicts with already chosen ones
	var checkViolations = func(paper []CandidateQuestion) bool {
		targetLexemes := make(map[string]bool)
		for _, q := range paper {
			if targetLexemes[q.LexemeID] {
				return true // duplicate target lexeme
			}
			targetLexemes[q.LexemeID] = true
		}

		for _, q := range paper {
			for otherQ := range paper {
				if paper[otherQ].ID == q.ID {
					continue
				}
				otherTarget := paper[otherQ].LexemeID

				// Distractor conflict
				for _, dl := range q.DistractorLexemeIDs {
					if dl == otherTarget {
						return true
					}
				}

				// Context stem conflict
				if q.TaskFamily == "context_selection" {
					for _, sl := range q.StemLexemeIDs {
						if sl == otherTarget {
							return true
						}
					}
				}
			}
		}
		return false
	}

	var backtrack func(taskIndex int) (bool, error)
	backtrack = func(taskIndex int) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}

		if taskIndex == len(requiredTasks) {
			return true, nil
		}

		steps++
		if steps > a.maxSearchSteps {
			return false, ErrAssemblyLimitReached
		}

		fam := requiredTasks[taskIndex]
		pool := byFamily[fam]

		for _, cand := range pool {
			// Fast target lexeme duplication check
			dup := false
			for _, prev := range chosen {
				if prev.ID == cand.ID || prev.LexemeID == cand.LexemeID {
					dup = true
					break
				}
			}
			if dup {
				continue
			}

			chosen = append(chosen, cand)
			if !checkViolations(chosen) {
				found, err := backtrack(taskIndex + 1)
				if err != nil {
					return false, err
				}
				if found {
					return true, nil
				}
			}
			chosen = chosen[:len(chosen)-1]
		}

		return false, nil
	}

	found, err := backtrack(0)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrConstraintUnsatisfied
	}

	return chosen, nil
}
