"""Prompt templates for generation and evaluation."""
import json
from typing import Any


def build_generator_prompt(
    fixed_input: dict[str, Any],
    task_family: str,
    feedback: str | None = None
) -> list[dict[str, str]]:
    canonical_form = fixed_input.get("canonical_form", "")
    pos = fixed_input.get("part_of_speech", "")
    meaning = fixed_input.get("target_meaning", "")

    system_msg = (
        "You are an expert lexicographer and test item creator. "
        "Generate a 4-choice multiple choice question in valid JSON. "
        "Output JSON with keys: task_family, stem, options (array of 4 items with text, is_correct, explanation), explanation. "
        "Exactly 1 option must have is_correct: true, and exactly 3 options must have is_correct: false."
    )

    user_content = (
        f"Target word: {canonical_form}\n"
        f"Part of speech: {pos}\n"
        f"Target meaning: {meaning}\n"
        f"Task family: {task_family}\n"
    )
    if feedback:
        user_content += (
            f"\nPrevious candidate was rejected with feedback:\n{feedback}\n"
            "Please revise the question to address the feedback and guarantee a single correct answer."
        )

    return [
        {"role": "system", "content": system_msg},
        {"role": "user", "content": user_content}
    ]


def build_evaluator_prompt(
    fixed_input: dict[str, Any],
    candidate_content: dict[str, Any],
    evaluator_index: int = 0
) -> list[dict[str, str]]:
    canonical_form = fixed_input.get("canonical_form", "")
    meaning = fixed_input.get("target_meaning", "")

    system_msg = (
        "You are an independent quality auditor for vocabulary questions. "
        "Evaluate the candidate question strictly and objectively. "
        "Audit all parts: stem, options, and overall explanation. "
        "If the question or explanation contains contradictions, false statements about word meaning, or invalid distractors, rate the affected criterion as reject. "
        "Output JSON with: profile_version, model, prompt_version, ratings (object mapping criteria to 'pass', 'reject', or 'uncertain'), rationale. "
        "Put 'ratings' first in the JSON object. Criteria to rate: target_meaning, naturalness, distractor_validity, unique_answer. "
        "Keep the rationale concise and under 60 words."
    )

    user_content = (
        f"Target word: {canonical_form}\n"
        f"Target meaning: {meaning}\n"
        f"Candidate question:\n{json.dumps(candidate_content, ensure_ascii=False, indent=2)}\n"
        "Audit this question. Check whether stem, options, and explanations are fully consistent, correct, and free of contradictions. If all criteria pass, return 'pass' for all ratings."
    )

    return [
        {"role": "system", "content": system_msg},
        {"role": "user", "content": user_content}
    ]
