"""Validate design artifacts and structural fixtures, without calling a model."""
from collections import Counter
import copy
import json
from pathlib import Path
import re
import unicodedata
from uuid import UUID
from check_exam_fixture import check_exam_cases

ROOT = Path(__file__).resolve().parents[1]


def require(condition, message):
    if not condition:
        raise ValueError(message)


def normalize(text):
    return " ".join(unicodedata.normalize("NFC", text).split()).casefold()


def structural_errors(question, senses, tasks):
    errors = set()
    options = question["options"]
    if len(options) != 4:
        errors.add("option_count")
    if len({o["id"] for o in options}) != len(options) or len({normalize(o["text"]) for o in options}) != len(options):
        errors.add("duplicate_option")
    if sum(o["id"] == question["correct_option_id"] for o in options) != 1:
        errors.add("invalid_answer_key")
    sense = senses.get(question["sense_id"])
    if not sense or sense["version_id"] != question["sense_version_id"]:
        errors.add("target_version")
    if question["task_family"] not in tasks:
        errors.add("task_family")
    if any(o["sense_id"] not in senses for o in options):
        errors.add("option_reference")
    return errors


def main():
    policy = json.loads((ROOT / "config/policy-v2.json").read_text())
    profile = json.loads((ROOT / "config/generation-profile.example.json").read_text())
    sources = json.loads((ROOT / "config/content-sources.json").read_text())
    fixture = json.loads((ROOT / "fixtures/content-v1.json").read_text())
    supplement = json.loads((ROOT / "fixtures/exam-assembly-v1.json").read_text())
    assembly_results = check_exam_cases(fixture, supplement, policy["task_families"])
    require(supplement["source_id"] in {s["id"] for s in sources["sources"]}, "Missing supplement provenance")
    fixture["questions"].extend(supplement["questions"])
    matrix = policy["observation_eligibility"]
    require(len(matrix["columns"]) == len(set(matrix["columns"])) == 6, "Invalid eligibility columns")
    for group, flags in matrix.items():
        if group != "columns":
            require(len(flags) == 6 and all(type(flag) is bool for flag in flags), "Invalid eligibility row: " + group)
    require(matrix["practice_early_review"] == matrix["practice_normal"], "Early practice must share ordinary eligibility")
    require(policy["selection"]["normal_position_purposes"] == ["normal", "early_review"], "Ordinary position purposes differ")
    require(policy["fsrs"] == json.loads((ROOT / "config/policy-v1.json").read_text())["fsrs"], "Unexpected FSRS change in review correction")
    require(policy["generation"]["enabled"] is False and profile["enabled"] is False, "Committed generation must be disabled")
    require(len(policy["fsrs"]["weights"]) == 21, "FSRS-6 needs 21 weights")
    require(policy["generation"]["lease_seconds"] > 2 * policy["generation"]["renew_every_seconds"], "Insufficient renewal margin")
    require(fixture["source_id"] in {s["id"] for s in sources["sources"]}, "Missing provenance")
    lexemes = {x["id"]: x for x in fixture["lexemes"]}
    senses = {x["id"]: x for x in fixture["senses"]}
    require(len(lexemes) == len(fixture["lexemes"]) and len(senses) == len(fixture["senses"]), "Duplicate identities")
    require(len({normalize(x["canonical_form"]) for x in lexemes.values()}) == len(lexemes), "Canonical collision")
    for sense in senses.values():
        UUID(sense["id"])
        UUID(sense["version_id"])
        require(sense["lexeme_id"] in lexemes, "Broken lexeme reference")
        require(sense["frequency"] is None, "Invented fixture frequency")
    for alias in fixture["aliases"]:
        require(alias["lexeme_id"] in lexemes, "Broken alias")
    for bank in fixture["banks"]:
        require(set(bank["sense_ids"]) <= senses.keys(), "Broken membership")
    questions = {q["id"]: q for q in fixture["questions"]}
    require(len(questions) == len(fixture["questions"]), "Duplicate question")
    require({q["task_family"] for q in questions.values()} == set(policy["task_families"]), "Missing task coverage")
    signatures = set()
    identity_by_key = {}
    key_by_identity = {}
    for q in questions.values():
        require(not structural_errors(q, senses, policy["task_families"]), "Invalid positive: " + q["id"])
        UUID(q["id"])
        answer = next(o for o in q["options"] if o["id"] == q["correct_option_id"])
        require(answer["sense_id"] == q["sense_id"], "Positive fixture answer mismatches target sense")
        for option in q["options"]:
            UUID(option["option_identity_id"])
            key = (option["language"], normalize(option["text"]), option["sense_id"], fixture["source_id"], fixture["source_version"])
            identity = option["option_identity_id"]
            require(identity_by_key.setdefault(key, identity) == identity, "Semantic option key must reuse its identity")
            require(key_by_identity.setdefault(identity, key) == key, "Semantic identity collision")
        signature = (q["sense_id"], q["task_family"], normalize(q["stem"]), tuple(sorted(o["sense_id"] for o in q["options"])))
        require(signature not in signatures, "Duplicate form")
        signatures.add(signature)
    require(max(Counter((q["sense_id"], q["task_family"]) for q in questions.values()).values()) >= 3, "Need initial plus two correction forms")
    require(max(Counter(s["lexeme_id"] for s in senses.values()).values()) >= 2, "Missing multi-sense case")
    structural_count = semantic_count = 0
    for case in fixture["negative_cases"]:
        q = copy.deepcopy(questions[case["base_question_id"]])
        mutation = case["mutation"]
        if mutation == "duplicate_first_option":
            q["options"][1] = copy.deepcopy(q["options"][0])
        elif mutation == "unknown_correct_option":
            q["correct_option_id"] = "00000000-0000-4000-8000-999999999999"
        elif mutation == "answer_second_option":
            q["correct_option_id"] = q["options"][1]["id"]
        elif mutation == "ambiguous_context":
            q["stem"] = "She drew a ___."
        elif mutation == "ungrammatical_context":
            q["stem"] = "The ___ are flows past the village yesterday."
        elif mutation == "conflicting_explanation":
            q["explanation"] = "river 表示山峰。"
        else:
            raise ValueError("Unknown mutation")
        errors = structural_errors(q, senses, policy["task_families"])
        if case["expected_structural_error"]:
            require(case["expected_structural_error"] in errors, "Negative not rejected: " + case["id"])
            structural_count += 1
        else:
            require(not errors and case["semantic_expected"] == "reject", "Invalid semantic fixture")
            semantic_count += 1
    links = 0
    for path in [ROOT / "README.md", *(ROOT / "docs").glob("*.md")]:
        for target in re.findall(r"\]\(([^)]+)\)", path.read_text()):
            if "://" in target or target.startswith("#"):
                continue
            require((path.parent / target.split("#")[0]).resolve().exists(), f"Broken link in {path.name}: {target}")
            links += 1
    print(json.dumps({"status": "ok", "lexemes": len(lexemes), "senses": len(senses), "valid_questions": len(questions),
                      "structural_negative_cases_checked": structural_count, "semantic_negative_cases_prepared_not_evaluated": semantic_count,
                      "documentation_links_checked": links, "exam_assembly_cases": assembly_results,
                      "design_version": policy["design_version"], "scope": "artifact integrity and fixture feasibility; not HTTP/business or production semantic acceptance"}))


if __name__ == "__main__":
    main()
