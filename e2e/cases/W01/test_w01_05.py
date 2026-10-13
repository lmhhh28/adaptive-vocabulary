"""W01-05: 展示审计去重、key换正文、延迟补交"""
import uuid
from support.http_client import APIClient
from support.web_harness import WebHarness


def run(context: dict) -> dict:
    with WebHarness(context, seed="core") as harness:
        bank_id = "00000000-0000-4000-8000-000000000801"
        client = APIClient(harness.base_url)
        client.initialize_client_context()

        try:
            # 1. Create session and answer Q0
            create_resp = client.post("/api/v1/practice-sessions", json={"bank_id": bank_id})
            assert create_resp.status_code == 201
            session_id = create_resp.json()["session"]["id"]

            next_resp = client.post(f"/api/v1/practice-sessions/{session_id}/next", json={"expected_cursor": 0})
            assert next_resp.status_code == 200
            q0 = next_resp.json()["question"]
            inst_id_0 = q0["instance_id"]

            att_resp = client.post(
                f"/api/v1/practice-sessions/{session_id}/attempts",
                json={"instance_id": inst_id_0, "option_id": q0["options"][0]["id"]},
            )
            assert att_resp.status_code == 200

            # 2. First feedback-view submission
            event_id_1 = str(uuid.uuid4())
            body_1 = {
                "client_event_id": event_id_1,
                "instance_id": inst_id_0,
                "presentation": "history",
                "client_displayed_at": "2026-09-12T03:30:00.000Z",
            }
            fv1_resp = client.post(
                f"/api/v1/practice-sessions/{session_id}/feedback-views",
                json=body_1,
                headers={"Idempotency-Key": event_id_1},
            )
            assert fv1_resp.status_code == 201, f"Expected 201, got {fv1_resp.status_code}: {fv1_resp.text}"
            fv1 = fv1_resp.json()
            assert fv1["view_id"]
            assert fv1["client_event_id"] == event_id_1
            assert fv1["event_sequence"] > 0
            assert fv1["recorded_at"]

            # 3. Exact replay with same key and body -> 200 OK + Idempotent-Replayed
            replay_resp = client.post(
                f"/api/v1/practice-sessions/{session_id}/feedback-views",
                json=body_1,
                headers={"Idempotency-Key": event_id_1},
            )
            assert replay_resp.status_code == 200, f"Expected 200 on replay, got {replay_resp.status_code}"
            assert replay_resp.headers.get("Idempotent-Replayed") == "true"
            fv_replay = replay_resp.json()
            assert fv_replay["view_id"] == fv1["view_id"]
            assert fv_replay["event_sequence"] == fv1["event_sequence"]
            assert fv_replay["recorded_at"] == fv1["recorded_at"]

            # 4. Same key with different body -> 409 IDEMPOTENCY_CONFLICT
            body_conflict = {
                "client_event_id": event_id_1,
                "instance_id": inst_id_0,
                "presentation": "restored_feedback",
                "client_displayed_at": "2026-09-12T03:35:00.000Z",
            }
            conflict_resp = client.post(
                f"/api/v1/practice-sessions/{session_id}/feedback-views",
                json=body_conflict,
                headers={"Idempotency-Key": event_id_1},
            )
            assert conflict_resp.status_code == 409, f"Expected 409 conflict, got {conflict_resp.status_code}"
            assert conflict_resp.json()["error"]["code"] == "IDEMPOTENCY_CONFLICT"

            # 5. Invalid presentation -> 400
            inv_resp = client.post(
                f"/api/v1/practice-sessions/{session_id}/feedback-views",
                json={
                    "client_event_id": str(uuid.uuid4()),
                    "instance_id": inst_id_0,
                    "presentation": "invalid_mode",
                },
            )
            assert inv_resp.status_code == 400

            # 6. Delayed submission after session ended
            det = client.get(f"/api/v1/practice-sessions/{session_id}").json()
            rev = det["session"]["revision"]
            end_resp = client.post(
                f"/api/v1/practice-sessions/{session_id}/end",
                json={"expected_revision": rev, "reason": "ended"},
            )
            assert end_resp.status_code == 200

            event_id_2 = str(uuid.uuid4())
            body_delayed = {
                "client_event_id": event_id_2,
                "instance_id": inst_id_0,
                "presentation": "restored_feedback",
                "client_displayed_at": "2026-09-12T03:40:00.000Z",
            }
            delayed_resp = client.post(
                f"/api/v1/practice-sessions/{session_id}/feedback-views",
                json=body_delayed,
                headers={"Idempotency-Key": event_id_2},
            )
            assert delayed_resp.status_code == 201, f"Expected 201 for delayed submission on ended session, got {delayed_resp.status_code}: {delayed_resp.text}"
            delayed_data = delayed_resp.json()
            assert delayed_data["client_event_id"] == event_id_2
            assert delayed_data["view_id"] != fv1["view_id"]
        finally:
            client.close()

        return {
            "status": "passed",
            "details": {
                "first_creation_201_verified": True,
                "idempotent_replay_200_verified": True,
                "body_conflict_409_verified": True,
                "delayed_submission_ended_session_verified": True,
            },
            "artifacts": harness.artifacts,
        }
