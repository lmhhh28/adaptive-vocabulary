export interface PendingWrite {
  schema_version: 1;
  operation_id: string;
  kind: "practice_attempt" | "practice_end" | "feedback_view" | string;
  session_id: string;
  method: "POST" | string;
  path: string;
  idempotency_key: string;
  body_text: string;
  state: "queued" | "sending" | "unknown" | "confirmed" | "rejected";
  created_at: string;
}

const DB_NAME = "avocab_outbox_db";
const STORE_NAME = "pending_writes";
const DB_VERSION = 1;

let dbInstance: IDBDatabase | null = null;
let isStorageFaulty = false;

export function simulateStorageFault(fault: boolean) {
  isStorageFaulty = fault;
}

export function openOutboxDB(): Promise<IDBDatabase> {
  if (isStorageFaulty) {
    return Promise.reject(new Error("StorageUnavailableError: IndexedDB quota or access denied"));
  }
  if (dbInstance) {
    return Promise.resolve(dbInstance);
  }
  return new Promise((resolve, reject) => {
    if (typeof indexedDB === "undefined") {
      reject(new Error("IndexedDB is not supported"));
      return;
    }
    const request = indexedDB.open(DB_NAME, DB_VERSION);
    request.onupgradeneeded = () => {
      const db = request.result;
      if (!db.objectStoreNames.contains(STORE_NAME)) {
        const store = db.createObjectStore(STORE_NAME, { keyPath: "operation_id" });
        store.createIndex("session_id", "session_id", { unique: false });
        store.createIndex("state", "state", { unique: false });
        store.createIndex("created_at", "created_at", { unique: false });
      }
    };
    request.onsuccess = () => {
      dbInstance = request.result;
      resolve(request.result);
    };
    request.onerror = () => {
      reject(request.error || new Error("Failed to open IndexedDB"));
    };
  });
}

export async function enqueueWrite(item: PendingWrite): Promise<void> {
  if (isStorageFaulty) {
    throw new Error("StorageUnavailableError: simulated storage failure");
  }
  const db = await openOutboxDB();
  return new Promise((resolve, reject) => {
    const tx = db.transaction(STORE_NAME, "readwrite");
    const store = tx.objectStore(STORE_NAME);
    const req = store.put(item);
    req.onsuccess = () => resolve();
    req.onerror = () => reject(req.error);
  });
}

export async function getPendingWrites(sessionId?: string): Promise<PendingWrite[]> {
  if (isStorageFaulty) {
    throw new Error("StorageUnavailableError");
  }
  const db = await openOutboxDB();
  return new Promise((resolve, reject) => {
    const tx = db.transaction(STORE_NAME, "readonly");
    const store = tx.objectStore(STORE_NAME);
    const req = store.getAll();
    req.onsuccess = () => {
      let results: PendingWrite[] = req.result || [];
      if (sessionId) {
        results = results.filter((w) => w.session_id === sessionId);
      }
      results.sort((a, b) => (a.created_at < b.created_at ? -1 : 1));
      resolve(results);
    };
    req.onerror = () => reject(req.error);
  });
}

export async function updateWriteState(
  operationId: string,
  state: PendingWrite["state"]
): Promise<void> {
  if (isStorageFaulty) {
    throw new Error("StorageUnavailableError");
  }
  const db = await openOutboxDB();
  return new Promise((resolve, reject) => {
    const tx = db.transaction(STORE_NAME, "readwrite");
    const store = tx.objectStore(STORE_NAME);
    const getReq = store.get(operationId);
    getReq.onsuccess = () => {
      const item: PendingWrite | undefined = getReq.result;
      if (!item) {
        resolve();
        return;
      }
      item.state = state;
      const putReq = store.put(item);
      putReq.onsuccess = () => resolve();
      putReq.onerror = () => reject(putReq.error);
    };
    getReq.onerror = () => reject(getReq.error);
  });
}

export async function removeWrite(operationId: string): Promise<void> {
  if (isStorageFaulty) {
    throw new Error("StorageUnavailableError");
  }
  const db = await openOutboxDB();
  return new Promise((resolve, reject) => {
    const tx = db.transaction(STORE_NAME, "readwrite");
    const store = tx.objectStore(STORE_NAME);
    const req = store.delete(operationId);
    req.onsuccess = () => resolve();
    req.onerror = () => reject(req.error);
  });
}

export async function clearAllWrites(): Promise<void> {
  const db = await openOutboxDB();
  return new Promise((resolve, reject) => {
    const tx = db.transaction(STORE_NAME, "readwrite");
    const store = tx.objectStore(STORE_NAME);
    const req = store.clear();
    req.onsuccess = () => resolve();
    req.onerror = () => reject(req.error);
  });
}
