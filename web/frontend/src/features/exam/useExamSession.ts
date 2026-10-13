import { useState, useEffect, useRef, useCallback } from "react";
import { message } from "antd";
import {
  ExamDetailResponse,
  ResponseEventRequest,
} from "./types";
import {
  getExamDetail,
  postResponseEvent,
  getEventReceipt,
  postTelemetryGap,
  submitExam,
  abandonExam,
  ApiError,
} from "./api";
import {
  enqueueWrite,
  getPendingWrites,
  updateWriteState,
  removeWrite,
  PendingWrite,
} from "../../api/outbox";
import { getSecureRandomUUID } from "../../utils/uuid";

export interface UseExamSessionOptions {
  examId: string;
  onNavigate?: (path: string) => void;
}

interface QueuedEvent {
  event: ResponseEventRequest;
  operationId: string;
  rebaseCount: number;
  hasAttemptedSend?: boolean;
}

export function useExamSession({ examId, onNavigate }: UseExamSessionOptions) {
  const [exam, setExam] = useState<ExamDetailResponse | null>(null);
  const [loading, setLoading] = useState<boolean>(true);
  const [error, setError] = useState<string | null>(null);

  // Drafts: instance_id -> selected_option_id
  const [drafts, setDrafts] = useState<Map<string, string | null>>(new Map());
  const [viewOrdinal, setViewOrdinal] = useState<number>(0);

  // Sync and Queue state
  const [pendingCount, setPendingCount] = useState<number>(0);
  const [hasUnknown, setHasUnknown] = useState<boolean>(false);
  const [isStorageAvailable, setIsStorageAvailable] = useState<boolean>(true);
  const [syncWarning, setSyncWarning] = useState<string | null>(null);

  // Submission & modals
  const [isSubmitting, setIsSubmitting] = useState<boolean>(false);
  const [submitModalVisible, setSubmitModalVisible] = useState<boolean>(false);
  const [abandonModalVisible, setAbandonModalVisible] = useState<boolean>(false);

  // Timer & Deadline
  const [remainingSeconds, setRemainingSeconds] = useState<number | null>(null);
  const [isExpired, setIsExpired] = useState<boolean>(false);
  const [deadlineSettling, setDeadlineSettling] = useState<boolean>(false);

  // Refs
  const sessionRevisionRef = useRef<number>(0);
  const lastAckSeqRef = useRef<number>(0);
  const eventQueueRef = useRef<QueuedEvent[]>([]);
  const isProcessingRef = useRef<boolean>(false);
  const isExpiredRef = useRef<boolean>(false);
  isExpiredRef.current = isExpired;
  const examRef = useRef<ExamDetailResponse | null>(null);
  examRef.current = exam;
  const processQueueRef = useRef<() => Promise<void>>(async () => {});
  const deadlineSettlementInFlightRef = useRef<boolean>(false);
  const deadlineRetryTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const deadlineRetryAttemptRef = useRef<number>(0);
  const queueGapPendingRef = useRef<boolean>(false);

  // Deadline expiration settlement
  const handleDeadlineExpire = useCallback(async (autoNavigate = true) => {
    if (deadlineSettlementInFlightRef.current) return;
    deadlineSettlementInFlightRef.current = true;
    if (deadlineRetryTimerRef.current) {
      clearTimeout(deadlineRetryTimerRef.current);
      deadlineRetryTimerRef.current = null;
    }
    setDeadlineSettling(true);
    const submissionId = getSecureRandomUUID();

    try {
      await submitExam(
        examId,
        {
          expected_revision: sessionRevisionRef.current,
          submission_id: submissionId,
          last_ack_sequence: lastAckSeqRef.current,
        },
        submissionId
      );
      deadlineRetryAttemptRef.current = 0;
      setSyncWarning(null);
      if (autoNavigate) {
        onNavigate?.(`/exams/${examId}/result`);
      }
    } catch (err: any) {
      if (err instanceof ApiError && err.code === "EXAM_SUBMITTED") {
        deadlineRetryAttemptRef.current = 0;
        setSyncWarning(null);
        if (autoNavigate) {
          onNavigate?.(`/exams/${examId}/result`);
        }
        return;
      }
      setSyncWarning("考试已到截止时间，正在自动重试确认交卷状态");
      deadlineRetryAttemptRef.current += 1;
      const delay = Math.min(1000 * 2 ** (deadlineRetryAttemptRef.current - 1), 15000);
      deadlineRetryTimerRef.current = setTimeout(() => {
        void handleDeadlineExpire(autoNavigate);
      }, delay);
    } finally {
      deadlineSettlementInFlightRef.current = false;
      setDeadlineSettling(false);
    }
  }, [examId, onNavigate]);

  // Initialize or load exam session
  useEffect(() => {
    let mounted = true;
    eventQueueRef.current = [];
    isProcessingRef.current = false;
    sessionRevisionRef.current = 0;
    lastAckSeqRef.current = 0;
    isExpiredRef.current = false;
    deadlineSettlementInFlightRef.current = false;
    deadlineRetryAttemptRef.current = 0;
    queueGapPendingRef.current = false;
    if (deadlineRetryTimerRef.current) {
      clearTimeout(deadlineRetryTimerRef.current);
      deadlineRetryTimerRef.current = null;
    }
    setExam(null);
    setLoading(true);
    setError(null);
    setDrafts(new Map());
    setViewOrdinal(0);
    setPendingCount(0);
    setHasUnknown(false);
    setSyncWarning(null);
    setSubmitModalVisible(false);
    setAbandonModalVisible(false);
    setRemainingSeconds(null);
    setIsExpired(false);
    setDeadlineSettling(false);

    getExamDetail(examId)
      .then(async (detail) => {
        if (!mounted) return;
        setExam(detail);
        sessionRevisionRef.current = detail.session_revision;
        lastAckSeqRef.current = detail.last_ack_sequence;

        // Initialize drafts map
        const draftMap = new Map<string, string | null>();
        for (const d of detail.drafts || []) {
          draftMap.set(d.instance_id, d.selected_option_id);
        }
        setDrafts(draftMap);

        // Merge any pending IndexedDB writes into drafts and queue
        try {
          const pendingWrites = await getPendingWrites(examId);
          const unresolved = pendingWrites.filter((w) => w.state === "unknown" || w.state === "queued" || w.state === "sending");
          let malformedCount = 0;
          for (const w of unresolved) {
            try {
              const evt = JSON.parse(w.body_text) as ResponseEventRequest;
              if (evt.action === "select" && evt.instance_id && evt.option_id) {
                draftMap.set(evt.instance_id, evt.option_id);
              } else if (evt.action === "clear" && evt.instance_id) {
                draftMap.set(evt.instance_id, null);
              }
              if (!eventQueueRef.current.some((q) => q.operationId === w.operation_id)) {
                eventQueueRef.current.push({
                  event: evt,
                  operationId: w.operation_id,
                  rebaseCount: 0,
                  hasAttemptedSend: w.state !== "queued",
                });
              }
            } catch {
              malformedCount++;
              queueGapPendingRef.current = true;
            }
          }
          if (eventQueueRef.current.length > 0 || malformedCount > 0) {
            setDrafts(new Map(draftMap));
            setHasUnknown(true);
            setPendingCount(eventQueueRef.current.length + malformedCount);
            if (malformedCount > 0) {
              setSyncWarning("检测到损坏的本地待同步记录，请点击重试同步以恢复");
            }
          }
        } catch {}

        if (detail.status === "submitted") {
          onNavigate?.(`/exams/${examId}/result`);
        }

        // Check deadline
        if (detail.deadline) {
          const deadlineTime = new Date(detail.deadline).getTime();
          const diff = Math.max(0, Math.floor((deadlineTime - Date.now()) / 1000));
          setRemainingSeconds(diff);
          if (diff <= 0) {
            setIsExpired(true);
            isExpiredRef.current = true;
            setSubmitModalVisible(false);
            if (detail.status === "open") {
              void handleDeadlineExpire(true);
            }
          }
        }

        if (eventQueueRef.current.length > 0 && detail.status === "open" && !isExpiredRef.current) {
          void processQueueRef.current();
        }
      })
      .catch((err) => {
        if (!mounted) return;
        setError(err.message || "Failed to load exam details");
      })
      .finally(() => {
        if (mounted) setLoading(false);
      });

    return () => {
      mounted = false;
      if (deadlineRetryTimerRef.current) {
        clearTimeout(deadlineRetryTimerRef.current);
        deadlineRetryTimerRef.current = null;
      }
    };
  }, [examId, onNavigate, handleDeadlineExpire]);

  // Timer countdown effect
  useEffect(() => {
    if (!exam?.deadline || exam.status !== "open" || isExpired) return;

    const timer = setInterval(() => {
      const deadlineTime = new Date(exam.deadline!).getTime();
      const diff = Math.max(0, Math.floor((deadlineTime - Date.now()) / 1000));
      setRemainingSeconds(diff);

      if (diff <= 0) {
        clearInterval(timer);
        setIsExpired(true);
        setSubmitModalVisible(false);
        handleDeadlineExpire();
      }
    }, 1000);

    return () => clearInterval(timer);
  }, [exam?.deadline, exam?.status, isExpired]);

  // Sequential queue processor
  const processQueue = useCallback(async () => {
    if (isProcessingRef.current) return;
    isProcessingRef.current = true;

    try {
      while (eventQueueRef.current.length > 0) {
        const item = eventQueueRef.current[0];

        // Check receipt first if recovering an item that had previously attempted send
        if (item.hasAttemptedSend) {
          try {
            const receipt = await getEventReceipt(examId, item.event.client_event_id);
            if (receipt && receipt.event_id) {
              sessionRevisionRef.current = Math.max(sessionRevisionRef.current, receipt.accepted_revision);
              lastAckSeqRef.current = Math.max(lastAckSeqRef.current, receipt.client_sequence);
              await removeWrite(item.operationId).catch(() => {});
              eventQueueRef.current.shift();
              setPendingCount(eventQueueRef.current.length);
              setHasUnknown(eventQueueRef.current.length > 0);
              setSyncWarning(null);
              continue;
            }
          } catch (receiptErr: any) {
            if (!(receiptErr instanceof ApiError && receiptErr.status === 404)) {
              await updateWriteState(item.operationId, "unknown").catch(() => {});
              setHasUnknown(true);
              setSyncWarning("无法确认已发送作答事件，请在网络恢复后点击“重试同步”");
              break;
            }
            // A definite 404 means this event has no receipt. Replay the exact
            // original request body with the same idempotency key.
          }
        } else if (!item.event.rebased_from) {
          item.event.expected_revision = sessionRevisionRef.current;
          item.event.client_sequence = lastAckSeqRef.current + 1;
        }

        item.hasAttemptedSend = true;

        try {
          await updateWriteState(item.operationId, "sending").catch(() => {});
          const res = await postResponseEvent(examId, item.event, item.operationId);

          // Success
          sessionRevisionRef.current = res.current_revision;
          lastAckSeqRef.current = res.accepted_sequence;
          await removeWrite(item.operationId).catch(() => {});

          eventQueueRef.current.shift();
          setPendingCount(eventQueueRef.current.length);
          setHasUnknown(false);
          setSyncWarning(null);
        } catch (err: any) {
          if (err instanceof ApiError) {
            if (
              err.status === 409 &&
              (err.code === "REVISION_CONFLICT" || err.code === "EVENT_SEQUENCE_CONFLICT") &&
              err.details?.acceptance === "not_accepted"
            ) {
              // Automatic rebase
              item.rebaseCount++;
              if (item.rebaseCount > 5) {
                await updateWriteState(item.operationId, "unknown").catch(() => {});
                setHasUnknown(true);
                setSyncWarning("版本冲突重试次数超限，请检查网络连接");
                break;
              }

              const serverRev = err.details.current_revision ?? sessionRevisionRef.current;
              const serverLastAck = err.details.last_ack_sequence;
              if (serverLastAck !== undefined && serverLastAck > lastAckSeqRef.current) {
                lastAckSeqRef.current = serverLastAck;
              }

              const oldSeq = item.event.client_sequence;
              const oldRev = item.event.expected_revision;

              sessionRevisionRef.current = serverRev;
              item.event.expected_revision = serverRev;
              item.event.client_sequence = lastAckSeqRef.current + 1;
              item.event.rebased_from = { client_sequence: oldSeq, expected_revision: oldRev };

              // Update persistent write with rebased payload
              await enqueueWrite({
                schema_version: 1,
                operation_id: item.operationId,
                kind: "exam_event",
                session_id: examId,
                method: "POST",
                path: `/api/v1/exams/${examId}/response-events`,
                idempotency_key: item.operationId,
                body_text: JSON.stringify(item.event),
                state: "queued",
                created_at: new Date().toISOString(),
              }).catch(() => {});

              // Retry next loop iteration
              continue;
            } else if (err.status === 409 && err.code === "EVENT_SEQUENCE_GAP") {
              const expectedNext = err.details?.expected_next_sequence || (lastAckSeqRef.current + 1);
              item.event.client_sequence = expectedNext;
              if (err.details?.current_revision !== undefined) {
                sessionRevisionRef.current = err.details.current_revision;
                item.event.expected_revision = err.details.current_revision;
              }
              continue;
            } else if (err.status === 409 && err.code === "EXAM_SUBMITTED") {
              // Already submitted on server
              eventQueueRef.current = [];
              setPendingCount(0);
              onNavigate?.(`/exams/${examId}/result`);
              break;
            }
          }

          // Network or unexpected failure
          await updateWriteState(item.operationId, "unknown").catch(() => {});
          setHasUnknown(true);
          setSyncWarning("作答事件提交未确认，正在重试同步");
          break;
        }
      }
    } finally {
      isProcessingRef.current = false;
      setPendingCount(eventQueueRef.current.length);
    }
  }, [examId, onNavigate]);

  processQueueRef.current = processQueue;

  // Queue event helper
  const enqueueEvent = useCallback(
    async (action: "select" | "clear", instanceId: string, optionId?: string) => {
      if (isExpiredRef.current) {
        message.warning("考试时间已截止，无法修改选项");
        return;
      }

      const clientEventId = getSecureRandomUUID();

      const nextSeq = lastAckSeqRef.current + eventQueueRef.current.length + 1;
      const eventReq: ResponseEventRequest = {
        instance_id: instanceId,
        action,
        option_id: optionId,
        client_event_id: clientEventId,
        client_sequence: nextSeq,
        expected_revision: sessionRevisionRef.current,
      };

      const outboxItem: PendingWrite = {
        schema_version: 1,
        operation_id: clientEventId,
        kind: "exam_event",
        session_id: examId,
        method: "POST",
        path: `/api/v1/exams/${examId}/response-events`,
        idempotency_key: clientEventId,
        body_text: JSON.stringify(eventReq),
        state: "queued",
        created_at: new Date().toISOString(),
      };

      try {
        await enqueueWrite(outboxItem);
        setIsStorageAvailable(true);
      } catch {
        setIsStorageAvailable(false);
        message.error("本地持久化存储不可用，请恢复存储权限后重试");
        return;
      }

      // Optimistic update of drafts
      setDrafts((prev) => {
        const next = new Map(prev);
        next.set(instanceId, action === "select" ? (optionId || null) : null);
        return next;
      });

      eventQueueRef.current.push({
        event: eventReq,
        operationId: clientEventId,
        rebaseCount: 0,
      });
      setPendingCount(eventQueueRef.current.length);

      processQueue();
    },
    [examId, processQueue]
  );

  // User selects an option
  const selectOption = useCallback(
    (instanceId: string, optionId: string) => {
      enqueueEvent("select", instanceId, optionId);
    },
    [enqueueEvent]
  );

  // User clears selection
  const clearSelection = useCallback(
    (instanceId: string) => {
      enqueueEvent("clear", instanceId);
    },
    [enqueueEvent]
  );

  // Navigation handlers
  const currentQuestions = exam?.public_questions || [];
  const currentQuestion = currentQuestions[viewOrdinal] || null;

  const handlePrev = useCallback(() => {
    if (viewOrdinal > 0) {
      setViewOrdinal((v) => v - 1);
    }
  }, [viewOrdinal]);

  const handleNext = useCallback(() => {
    if (viewOrdinal < currentQuestions.length - 1) {
      setViewOrdinal((v) => v + 1);
    }
    // Note: on the last question, handleNext does NOTHING and never submits.
  }, [viewOrdinal, currentQuestions.length]);

  const handleJump = useCallback((ordinal: number) => {
    setViewOrdinal(ordinal);
  }, []);

  // Keyboard shortcut bindings
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (["INPUT", "TEXTAREA"].includes((e.target as HTMLElement)?.tagName)) {
        return;
      }

      if (e.repeat || e.ctrlKey || e.metaKey || e.altKey) return;

      if (submitModalVisible || abandonModalVisible || isSubmitting || deadlineSettling) {
        if (e.key === "Escape") {
          setSubmitModalVisible(false);
          setAbandonModalVisible(false);
        }
        return;
      }

      if (e.key === "a" || e.key === "A") {
        handlePrev();
      } else if (e.key === "d" || e.key === "D") {
        handleNext();
      } else if (e.key === "Escape") {
        setSubmitModalVisible(false);
        setAbandonModalVisible(false);
      }
      // Note: Enter on question blank does nothing (EX-07).
    };

    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [handlePrev, handleNext, submitModalVisible, abandonModalVisible, isSubmitting, deadlineSettling]);

  // Click Submit Exam button: check queue barrier first
  const requestSubmitModal = useCallback(() => {
    if (isExpiredRef.current) {
      void handleDeadlineExpire(true);
      return;
    }
    if (eventQueueRef.current.length > 0 || hasUnknown || pendingCount > 0) {
      setSyncWarning("当前有未确认或正在同步的作答事件，请稍等同步完成后再交卷");
      message.warning("正在同步作答事件，请稍候再试");
      return;
    }
    setSubmitModalVisible(true);
  }, [hasUnknown, pendingCount, handleDeadlineExpire]);

  // Confirm and perform actual submission
  const confirmSubmit = useCallback(async () => {
    if (isSubmitting) return;
    setIsSubmitting(true);

    const submissionId = getSecureRandomUUID();

    try {
      await submitExam(
        examId,
        {
          expected_revision: sessionRevisionRef.current,
          submission_id: submissionId,
          last_ack_sequence: lastAckSeqRef.current,
        },
        submissionId
      );

      setSubmitModalVisible(false);
      onNavigate?.(`/exams/${examId}/result`);
    } catch (err: any) {
      if (err instanceof ApiError && err.code === "EXAM_SUBMITTED") {
        setSubmitModalVisible(false);
        onNavigate?.(`/exams/${examId}/result`);
        return;
      }
      message.error(err.message || "交卷失败，请重试");
    } finally {
      setIsSubmitting(false);
    }
  }, [examId, isSubmitting, onNavigate]);

  // Confirm Abandon Exam
  const confirmAbandon = useCallback(async () => {
    try {
      const res = await abandonExam(examId, {
        expected_revision: sessionRevisionRef.current,
      });
      setAbandonModalVisible(false);
      if (res.result_url || res.status === "submitted" || res.status === "abandoned") {
        onNavigate?.(`/exams/${examId}/result`);
      } else {
        onNavigate?.("/exams/new");
      }
    } catch (err: any) {
      if (err instanceof ApiError && err.code === "EXAM_SUBMITTED") {
        setAbandonModalVisible(false);
        onNavigate?.(`/exams/${examId}/result`);
        return;
      }
      message.error(err.message || "放弃考试失败");
    }
  }, [examId, onNavigate]);

  // Manual retry of pending events
  const retrySync = useCallback(async () => {
    try {
      const writes = await getPendingWrites(examId);
      const unresolved = writes.filter((w) => w.state === "unknown" || w.state === "queued" || w.state === "sending");
      if (unresolved.length === 0) {
        setHasUnknown(false);
        setSyncWarning(null);
        return;
      }
      // Rebuild memory queue from IndexedDB pending writes
      const existingIds = new Set(eventQueueRef.current.map((q) => q.operationId));
      const malformedIds: string[] = [];
      for (const w of unresolved) {
        if (!existingIds.has(w.operation_id)) {
          try {
            const event = JSON.parse(w.body_text) as ResponseEventRequest;
            eventQueueRef.current.push({
              event,
              operationId: w.operation_id,
              rebaseCount: 0,
              hasAttemptedSend: w.state !== "queued",
            });
          } catch {
            malformedIds.push(w.operation_id);
          }
        }
      }
      for (const operationId of malformedIds) {
        await removeWrite(operationId).catch(() => {});
      }
      if (malformedIds.length > 0) {
        queueGapPendingRef.current = true;
      }
      setPendingCount(eventQueueRef.current.length + malformedIds.length);
      await processQueue();

      if (queueGapPendingRef.current && eventQueueRef.current.length === 0) {
        const res = await postTelemetryGap(
          examId,
          {
            expected_revision: sessionRevisionRef.current,
            reason: "lost_queue",
          },
          `gap-${getSecureRandomUUID()}`
        );
        sessionRevisionRef.current = res.current_revision;
        lastAckSeqRef.current = res.last_ack_sequence;
        setPendingCount(0);
        setHasUnknown(false);
        setSyncWarning(null);
        queueGapPendingRef.current = false;
        message.warning("损坏的本地记录已隔离，并已标记同步间隙");
      } else if (malformedIds.length > 0) {
        setSyncWarning("损坏的本地记录已隔离；其余作答事件仍待确认");
      }
    } catch {
      message.error("无法读取待同步队列");
    }
  }, [examId, processQueue]);

  // Telemetry gap report (e.g. if queue was lost)
  const reportQueueLost = useCallback(async () => {
    try {
      const res = await postTelemetryGap(
        examId,
        {
          expected_revision: sessionRevisionRef.current,
          reason: "lost_queue",
        },
        `gap-${Date.now()}`
      );
      sessionRevisionRef.current = res.current_revision;
      lastAckSeqRef.current = res.last_ack_sequence;
      eventQueueRef.current = [];
      queueGapPendingRef.current = false;
      setPendingCount(0);
      setHasUnknown(false);
      setSyncWarning(null);
      message.info("已标记遥测间隙并恢复同步水位");
    } catch (err: any) {
      message.error("间隙恢复失败: " + err.message);
    }
  }, [examId]);

  // Calculate answered count
  let answeredCount = 0;
  for (const q of currentQuestions) {
    if (drafts.get(q.instance_id)) {
      answeredCount++;
    }
  }
  const totalCount = currentQuestions.length;
  const unansweredCount = totalCount - answeredCount;

  return {
    exam,
    loading,
    error,
    currentQuestions,
    currentQuestion,
    viewOrdinal,
    drafts,
    selectedOptionId: currentQuestion ? drafts.get(currentQuestion.instance_id) || undefined : undefined,
    pendingCount,
    hasUnknown,
    isStorageAvailable,
    syncWarning,
    isSubmitting,
    submitModalVisible,
    abandonModalVisible,
    remainingSeconds,
    isExpired,
    deadlineSettling,
    handleDeadlineExpire,
    answeredCount,
    totalCount,
    unansweredCount,
    selectOption,
    clearSelection,
    handlePrev,
    handleNext,
    handleJump,
    requestSubmitModal,
    setSubmitModalVisible,
    confirmSubmit,
    setAbandonModalVisible,
    confirmAbandon,
    retrySync,
    reportQueueLost,
  };
}
