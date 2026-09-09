"""Exhaustive oracle for the small authored fixture, not the application assembler."""
from collections import Counter
from itertools import combinations
import re


def check_exam_cases(base, supplement, tasks):
    senses = {s["id"]: s for s in base["senses"]}
    lexemes = {x["id"]: x for x in base["lexemes"]}
    forms = {x["canonical_form"].casefold(): x["id"] for x in lexemes.values()}
    forms.update({x["form"].casefold(): x["lexeme_id"] for x in base["aliases"]})
    all_questions = {q["id"]: q for q in base["questions"] + supplement["questions"]}

    def violations(paper):
        targets = [senses[q["sense_id"]]["lexeme_id"] for q in paper]
        errors = set()
        if Counter(q["task_family"] for q in paper) != Counter(tasks):
            errors.add("quota")
        if len(set(targets)) != len(targets):
            errors.add("lexeme_dedup")
        for q, target in zip(paper, targets):
            other_targets = set(targets) - {target}
            for option in q["options"]:
                if (option["language"] == "en" and option["id"] != q["correct_option_id"]
                        and senses[option["sense_id"]]["lexeme_id"] in other_targets):
                    errors.add("cross_question_constraint")
            if q["task_family"] == "context_selection":
                tokens = re.findall(r"[a-z]+(?:['-][a-z]+)*", q["stem"].casefold())
                if any(forms.get(token) in other_targets for token in tokens):
                    errors.add("cross_question_constraint")
        return errors

    results = []
    for case in supplement["cases"]:
        pool = base["questions"] if case["pool"] == "base" else list(all_questions.values())
        if "only_question_ids" in case:
            pool = [all_questions[qid] for qid in case["only_question_ids"]]
        feasible = [paper for paper in combinations(pool, 3) if not violations(paper)]
        if bool(feasible) != case["expected_feasible"]:
            raise ValueError("Assembly fixture expectation failed: " + case["id"])
        if "witness" in case and violations([all_questions[qid] for qid in case["witness"]]):
            raise ValueError("Invalid assembly witness: " + case["id"])
        if "expected_violation" in case and case["expected_violation"] not in violations(pool):
            raise ValueError("Negative case fails for the wrong reason: " + case["id"])
        results.append({"id": case["id"], "feasible_papers": len(feasible)})
    return results
