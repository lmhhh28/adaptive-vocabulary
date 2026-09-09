"""Background lease manager to automatically renew active claimed generation items."""
from contextlib import contextmanager
import threading
import time
from typing import Optional
from .go_client import GoGenerationClient, LeaseLostError


class LeaseTracker:
    def __init__(self, item_id: str, fencing_token: int, input_digest: str):
        self.item_id = item_id
        self.fencing_token = fencing_token
        self.input_digest = input_digest
        self.lease_lost = False
        self.last_renew_time = time.time()
        self.last_error: Optional[Exception] = None


class LeaseManager:
    def __init__(self, go_client: GoGenerationClient, renew_interval_seconds: float = 30.0):
        self.go_client = go_client
        self.renew_interval = renew_interval_seconds
        self._tracked: dict[str, LeaseTracker] = {}
        self._lock = threading.Lock()
        self._stop_event = threading.Event()
        self._thread = threading.Thread(target=self._renewal_loop, daemon=True)
        self._thread.start()

    def close(self):
        self._stop_event.set()
        self._thread.join(timeout=2.0)

    @contextmanager
    def track(self, item_id: str, fencing_token: int, input_digest: str):
        tracker = LeaseTracker(item_id, fencing_token, input_digest)
        with self._lock:
            self._tracked[item_id] = tracker
        try:
            yield tracker
        finally:
            with self._lock:
                self._tracked.pop(item_id, None)

    def is_lease_lost(self, item_id: str) -> bool:
        with self._lock:
            tracker = self._tracked.get(item_id)
            return tracker.lease_lost if tracker else False

    def _renewal_loop(self):
        while not self._stop_event.is_set():
            time.sleep(min(1.0, self.renew_interval))
            now = time.time()

            with self._lock:
                items_to_renew = [
                    t for t in self._tracked.values()
                    if not t.lease_lost and (now - t.last_renew_time) >= self.renew_interval
                ]

            for tracker in items_to_renew:
                try:
                    self.go_client.renew(tracker.item_id, tracker.fencing_token, tracker.input_digest)
                    tracker.last_renew_time = now
                except LeaseLostError as exc:
                    tracker.lease_lost = True
                    tracker.last_error = exc
                except Exception as exc:
                    tracker.last_error = exc
