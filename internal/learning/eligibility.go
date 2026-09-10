package learning

// ClassifyEligibility evaluates an attempt's eligibility flags according to R05 and policy-v2
func ClassifyEligibility(attempt *AttemptEvent, isDisputedOrInvalidated bool) EligibilityDecision {
	if isDisputedOrInvalidated {
		return EligibilityDecision{
			IsValid:             false,
			OrdinaryEvidence:    false,
			OrdinaryConfusion:   false,
			UpdateConsolidation: false,
			NormalGap:           false,
			CorrectionGap:       false,
			FSRSReview:          false,
		}
	}

	switch attempt.Source {
	case SourcePractice:
		switch attempt.Purpose {
		case PurposeNormal:
			return EligibilityDecision{
				IsValid:             true,
				OrdinaryEvidence:    true,
				OrdinaryConfusion:   true,
				UpdateConsolidation: true,
				NormalGap:           true,
				CorrectionGap:       true,
				FSRSReview:          true,
			}
		case PurposeEarlyReview:
			return EligibilityDecision{
				IsValid:             true,
				OrdinaryEvidence:    true,
				OrdinaryConfusion:   true,
				UpdateConsolidation: true,
				NormalGap:           true,
				CorrectionGap:       true,
				FSRSReview:          true,
			}
		case PurposeCorrection:
			return EligibilityDecision{
				IsValid:             true,
				OrdinaryEvidence:    false,
				OrdinaryConfusion:   false,
				UpdateConsolidation: false,
				NormalGap:           false,
				CorrectionGap:       true,
				FSRSReview:          true,
			}
		default:
			return EligibilityDecision{}
		}

	case SourceExam:
		switch attempt.ResponseHistory {
		case ResponseSingleResponse:
			return EligibilityDecision{
				IsValid:             true,
				OrdinaryEvidence:    true,
				OrdinaryConfusion:   true,
				UpdateConsolidation: true,
				NormalGap:           false,
				CorrectionGap:       false,
				FSRSReview:          true,
			}
		case ResponseRevised:
			return EligibilityDecision{
				IsValid:             true,
				OrdinaryEvidence:    false,
				OrdinaryConfusion:   false,
				UpdateConsolidation: false,
				NormalGap:           false,
				CorrectionGap:       false,
				FSRSReview:          false,
			}
		case ResponseTelemetryIncomplete:
			return EligibilityDecision{
				IsValid:             true,
				OrdinaryEvidence:    false,
				OrdinaryConfusion:   false,
				UpdateConsolidation: false,
				NormalGap:           false,
				CorrectionGap:       false,
				FSRSReview:          false,
			}
		default:
			return EligibilityDecision{}
		}

	default:
		return EligibilityDecision{}
	}
}
