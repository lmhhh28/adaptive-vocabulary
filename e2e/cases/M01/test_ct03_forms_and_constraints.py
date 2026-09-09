"""CT-03: Form vs version identity, and structural constraints acceptance test.
Verifies punctuation/answer-key revisions preserve form_id and create new version_id ->
semantic modifications create new form_id -> structural errors (duplicate options, unknown sense) are rejected.
"""
from pathlib import Path
import sqlite3
import uuid
from support.lifecycle import managed_server
from support.http_client import APIClient


def run(context: dict) -> dict:
    bin_path: Path = context["bin_path"]
    case_dir: Path = context["case_dir"]
    data_dir = case_dir / "data"
    data_dir.mkdir(parents=True, exist_ok=True)
    artifacts = []

    with managed_server(bin_path, data_dir, listen_port=0, seed="core", log_dir=case_dir) as server:
        base_url = server.base_url
        artifacts.append(str(server.log_path))

        client = APIClient(base_url)
        csrf_token = client.initialize_client_context()

        # Known fixture IDs
        sense_river = "00000000-0000-4000-8000-000000000101"
        sense_ver_river = "00000000-0000-4000-8000-000000000201"
        sense_mountain = "00000000-0000-4000-8000-000000000102"
        sense_window = "00000000-0000-4000-8000-000000000103"
        sense_desert = "00000000-0000-4000-8000-000000000104"
        sense_cloud = "00000000-0000-4000-8000-000000000106"

        standard_options = [
            {"id": str(uuid.uuid4()), "text": "河流", "sense_id": sense_river, "language": "zh", "option_identity_id": str(uuid.uuid4())},
            {"id": str(uuid.uuid4()), "text": "山峰", "sense_id": sense_mountain, "language": "zh", "option_identity_id": str(uuid.uuid4())},
            {"id": str(uuid.uuid4()), "text": "窗户", "sense_id": sense_window, "language": "zh", "option_identity_id": str(uuid.uuid4())},
            {"id": str(uuid.uuid4()), "text": "沙漠", "sense_id": sense_desert, "language": "zh", "option_identity_id": str(uuid.uuid4())},
        ]

        # 1. Structural Rejection Tests
        # Check A: Option count != 4 rejected
        bad_count_draft = {
            "sense_id": sense_river,
            "sense_version_id": sense_ver_river,
            "task_family": "meaning_recognition",
            "stem": "river 的中文意思是？",
            "options": standard_options[:3],
            "correct_option_id": standard_options[0]["id"],
            "explanation": "test",
        }
        res_bad_count = client.post("/api/v1/test/questions", json=bad_count_draft)
        assert res_bad_count.status_code == 400, f"Expected 400 for option count != 4, got {res_bad_count.status_code}"

        # Check B: Duplicate option texts rejected
        dup_text_options = [
            {"id": str(uuid.uuid4()), "text": "河流", "sense_id": sense_river, "language": "zh", "option_identity_id": str(uuid.uuid4())},
            {"id": str(uuid.uuid4()), "text": "河流", "sense_id": sense_mountain, "language": "zh", "option_identity_id": str(uuid.uuid4())},
            {"id": str(uuid.uuid4()), "text": "窗户", "sense_id": sense_window, "language": "zh", "option_identity_id": str(uuid.uuid4())},
            {"id": str(uuid.uuid4()), "text": "沙漠", "sense_id": sense_desert, "language": "zh", "option_identity_id": str(uuid.uuid4())},
        ]
        dup_text_draft = {
            "sense_id": sense_river,
            "sense_version_id": sense_ver_river,
            "task_family": "meaning_recognition",
            "stem": "river 的中文意思是？",
            "options": dup_text_options,
            "correct_option_id": dup_text_options[0]["id"],
            "explanation": "test",
        }
        res_dup_text = client.post("/api/v1/test/questions", json=dup_text_draft)
        assert res_dup_text.status_code == 400, f"Expected 400 for duplicate option text, got {res_dup_text.status_code}"

        # Check C: Duplicate option_identity_id rejected
        dup_id_options = [
            {"id": str(uuid.uuid4()), "text": "河流", "sense_id": sense_river, "language": "zh", "option_identity_id": "00000000-0000-4000-8000-000000009999"},
            {"id": str(uuid.uuid4()), "text": "山峰", "sense_id": sense_mountain, "language": "zh", "option_identity_id": "00000000-0000-4000-8000-000000009999"},
            {"id": str(uuid.uuid4()), "text": "窗户", "sense_id": sense_window, "language": "zh", "option_identity_id": str(uuid.uuid4())},
            {"id": str(uuid.uuid4()), "text": "沙漠", "sense_id": sense_desert, "language": "zh", "option_identity_id": str(uuid.uuid4())},
        ]
        dup_id_draft = {
            "sense_id": sense_river,
            "sense_version_id": sense_ver_river,
            "task_family": "meaning_recognition",
            "stem": "river 的中文意思是？",
            "options": dup_id_options,
            "correct_option_id": dup_id_options[0]["id"],
            "explanation": "test",
        }
        res_dup_id = client.post("/api/v1/test/questions", json=dup_id_draft)
        assert res_dup_id.status_code == 400, f"Expected 400 for duplicate option_identity_id, got {res_dup_id.status_code}"

        # Check D: Unknown option sense_id rejected
        bad_sense_options = [
            {"id": str(uuid.uuid4()), "text": "河流", "sense_id": "00000000-0000-4000-8000-999999999999", "language": "zh", "option_identity_id": str(uuid.uuid4())},
            {"id": str(uuid.uuid4()), "text": "山峰", "sense_id": sense_mountain, "language": "zh", "option_identity_id": str(uuid.uuid4())},
            {"id": str(uuid.uuid4()), "text": "窗户", "sense_id": sense_window, "language": "zh", "option_identity_id": str(uuid.uuid4())},
            {"id": str(uuid.uuid4()), "text": "沙漠", "sense_id": sense_desert, "language": "zh", "option_identity_id": str(uuid.uuid4())},
        ]
        bad_sense_draft = {
            "sense_id": sense_river,
            "sense_version_id": sense_ver_river,
            "task_family": "meaning_recognition",
            "stem": "river 的中文意思是？",
            "options": bad_sense_options,
            "correct_option_id": bad_sense_options[0]["id"],
            "explanation": "test",
        }
        res_bad_sense = client.post("/api/v1/test/questions", json=bad_sense_draft)
        assert res_bad_sense.status_code == 400, f"Expected 400 for unknown option sense, got {res_bad_sense.status_code}"

        # 2. Form vs Version Identity Tests
        # Get existing Question 1 form_id from seed
        db_file = data_dir / "app.db"
        conn = sqlite3.connect(str(db_file))
        cur = conn.cursor()
        cur.execute("SELECT id, form_id, stem FROM question_version WHERE id = '00000000-0000-4000-8000-000000000301'")
        orig_row = cur.fetchone()
        assert orig_row is not None
        orig_version_id, orig_form_id, orig_stem = orig_row

        # Revision 1: Punctuation fix on stem ("river 的中文意思是？" -> "river 的中文意思是?")
        # Normalized text collapses whitespace and case folds. Same options.
        rev1_options = [
            {"id": str(uuid.uuid4()), "text": "河流", "sense_id": sense_river, "language": "zh", "option_identity_id": "00000000-0000-4000-8000-000000004101"},
            {"id": str(uuid.uuid4()), "text": "山峰", "sense_id": sense_mountain, "language": "zh", "option_identity_id": "00000000-0000-4000-8000-000000004102"},
            {"id": str(uuid.uuid4()), "text": "窗户", "sense_id": sense_window, "language": "zh", "option_identity_id": "00000000-0000-4000-8000-000000004103"},
            {"id": str(uuid.uuid4()), "text": "沙漠", "sense_id": sense_desert, "language": "zh", "option_identity_id": "00000000-0000-4000-8000-000000004104"},
        ]
        rev1_draft = {
            "sense_id": sense_river,
            "sense_version_id": sense_ver_river,
            "task_family": "meaning_recognition",
            "stem": "river 的中文意思是?",
            "parent_form_id": orig_form_id,
            "options": rev1_options,
            "correct_option_id": rev1_options[0]["id"],
            "explanation": "river 表示河流（标点微调修订）。",
        }
        res_rev1 = client.post("/api/v1/test/questions", json=rev1_draft)
        assert res_rev1.status_code == 201, f"Expected 201, got {res_rev1.status_code}: {res_rev1.text}"
        rev1_data = res_rev1.json()

        # MUST retain the same form_id, but produce a NEW version_id!
        assert rev1_data["form_id"] == orig_form_id, f"Expected form_id {orig_form_id}, got {rev1_data['form_id']}"
        assert rev1_data["version_id"] != orig_version_id, "Expected new version_id for revision"

        # Revision 2: Answer key correction (same stem, same options, but change correct_option_id)
        rev2_options = [
            {"id": str(uuid.uuid4()), "text": "河流", "sense_id": sense_river, "language": "zh", "option_identity_id": "00000000-0000-4000-8000-000000004101"},
            {"id": str(uuid.uuid4()), "text": "山峰", "sense_id": sense_mountain, "language": "zh", "option_identity_id": "00000000-0000-4000-8000-000000004102"},
            {"id": str(uuid.uuid4()), "text": "窗户", "sense_id": sense_window, "language": "zh", "option_identity_id": "00000000-0000-4000-8000-000000004103"},
            {"id": str(uuid.uuid4()), "text": "沙漠", "sense_id": sense_desert, "language": "zh", "option_identity_id": "00000000-0000-4000-8000-000000004104"},
        ]
        rev2_draft = {
            "sense_id": sense_river,
            "sense_version_id": sense_ver_river,
            "task_family": "meaning_recognition",
            "stem": "river 的中文意思是？",
            "options": rev2_options,
            "correct_option_id": rev2_options[1]["id"],  # Changed answer key
            "explanation": "纠错答案键修订测试。",
        }
        res_rev2 = client.post("/api/v1/test/questions", json=rev2_draft)
        assert res_rev2.status_code == 201, f"Expected 201, got {res_rev2.status_code}: {res_rev2.text}"
        rev2_data = res_rev2.json()

        # MUST retain the same form_id!
        assert rev2_data["form_id"] == orig_form_id, f"Expected form_id {orig_form_id}, got {rev2_data['form_id']}"
        assert rev2_data["version_id"] != orig_version_id

        # Revision 3: Semantic change (replace distractor "沙漠" with "云" / sense_cloud)
        rev3_options = [
            {"id": str(uuid.uuid4()), "text": "河流", "sense_id": sense_river, "language": "zh", "option_identity_id": "00000000-0000-4000-8000-000000004101"},
            {"id": str(uuid.uuid4()), "text": "山峰", "sense_id": sense_mountain, "language": "zh", "option_identity_id": "00000000-0000-4000-8000-000000004102"},
            {"id": str(uuid.uuid4()), "text": "窗户", "sense_id": sense_window, "language": "zh", "option_identity_id": "00000000-0000-4000-8000-000000004103"},
            {"id": str(uuid.uuid4()), "text": "云", "sense_id": sense_cloud, "language": "zh", "option_identity_id": str(uuid.uuid4())},
        ]
        rev3_draft = {
            "sense_id": sense_river,
            "sense_version_id": sense_ver_river,
            "task_family": "meaning_recognition",
            "stem": "river 的中文意思是？",
            "options": rev3_options,
            "correct_option_id": rev3_options[0]["id"],
            "explanation": "语义选项替换测试。",
        }
        res_rev3 = client.post("/api/v1/test/questions", json=rev3_draft)
        assert res_rev3.status_code == 201, f"Expected 201, got {res_rev3.status_code}: {res_rev3.text}"
        rev3_data = res_rev3.json()

        # MUST generate a NEW form_id!
        assert rev3_data["form_id"] != orig_form_id, f"Expected new form_id for semantic change, but got original {orig_form_id}"

        # Verify in database: exactly 15 forms now (14 seed forms + 1 new form from rev3)
        cur.execute("SELECT COUNT(*) FROM question_form")
        final_form_count = cur.fetchone()[0]
        assert final_form_count == 15, f"Expected 15 forms in DB, got {final_form_count}"

        conn.close()
        client.close()

    return {
        "status": "passed",
        "artifacts": artifacts,
        "details": {
            "structural_rejections": "verified",
            "punctuation_retains_form": "verified",
            "answer_key_retains_form": "verified",
            "semantic_change_creates_form": "verified",
        },
    }
