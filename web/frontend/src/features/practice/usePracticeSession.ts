import { useState, useEffect, useRef, useCallback } from "react";
import { message } from "antd";
import {
  PublicQuestion,
  AttemptFeedback,
  AcceptedAnswer,
  AnswerRecord,
  PracticeSessionSummary,
  PracticeSession,
  FeedbackViewRequest,
} from "./types";
import {
  createPracticeSession,
  getPracticeSession,
  fetchNextQuestion,
  submitAttempt,
  getAnswerRecords,
  getAnsweredQuestion,
  submitFeedbackView,
  endPracticeSession,
  getAvailableBanks,
} from "./api";
import {
  enqueueWrite,
  getPendingWrites,
  updateWriteState,
  PendingWrite,
} from "../../api/outbox";
import { fetchClientContext } from "../../api/clientContext";
import { getSecureRandomUUID } from "../../utils/uuid";

export type PracticeUIStatus =
  | "loading"
  | "unanswered"
  | "submitting"
  | "submit-unknown"
  | "correct-feedback"
  | "incorrect-feedback"
  | "reviewing"
  | "loading-next"
  | "no-candidates"
  | "ended"
  | "active-conflict";

const AUTO_ADVANCE_STORAGE_KEY = "avocab.ui.practice.autoAdvanceCorrect.v1";

export function usePracticeSession(bankIdFromUrl?: string, sessionIdFromUrl?: string, restartToken?: string) {
  const mountedRef = useRef<boolean>(true);
  // Session & Question state
  const [session, setSession] = useState<PracticeSession | null>(null);
  const [activeQuestion, setActiveQuestion] = useState<PublicQuestion | null>(null);
  const [activeFeedback, setActiveFeedback] = useState<AttemptFeedback | null>(null);
  const [selectedOptionId, setSelectedOptionId] = useState<string | null>(null);

  // Viewing / navigation state (viewOrdinal can differ from activeQuestion.ordinal when reviewing)
  const [viewOrdinal, setViewOrdinal] = useState<number>(0);
  const [viewAnswer, setViewAnswer] = useState<AcceptedAnswer | null>(null);

  // Answer records & summary
  const [records, setRecords] = useState<AnswerRecord[]>([]);
  const [summary, setSummary] = useState<PracticeSessionSummary>({
    counts_scope: "accepted_attempts",
    answered_count: 0,
    correct_count: 0,
    incorrect_count: 0,
  });

  // UI state
  const [uiStatus, setUiStatus] = useState<PracticeUIStatus>("loading");
  const [statusMessage, setStatusMessage] = useState<string>("");
  const [pendingWritesCount, setPendingWritesCount] = useState<number>(0);
  const [hasUnknownWrite, setHasUnknownWrite] = useState<boolean>(false);
  const [isStorageAvailable, setIsStorageAvailable] = useState<boolean>(true);

  // Auto-advance
  const [autoAdvanceEnabled, setAutoAdvanceEnabled] = useState<boolean>(() => {
    try {
      return localStorage.getItem(AUTO_ADVANCE_STORAGE_KEY) !== "false";
    } catch {
      return true;
    }
  });
  const [timerCancelled, setTimerCancelled] = useState<boolean>(false);

  // Modals & conflict
  const [endModalVisible, setEndModalVisible] = useState<boolean>(false);
  const [conflictSessionId, setConflictSessionId] = useState<string | null>(null);
  const [availableBanks, setAvailableBanks] = useState<Array<{ id: string; name: string }>>([]);

  // Refs for race conditions, mutexes and stale closures
  const autoAdvanceTimerRef = useRef<any>(null);
  const submittingLockRef = useRef<boolean>(false);
  const advancingLockRef = useRef<boolean>(false);
  const answeredAnswersCache = useRef<Map<number, AcceptedAnswer>>(new Map());
  const lastPendingWriteRef = useRef<PendingWrite | null>(null);

  const sessionRef = useRef<PracticeSession | null>(session);
  sessionRef.current = session;
  const activeQuestionRef = useRef<PublicQuestion | null>(activeQuestion);
  activeQuestionRef.current = activeQuestion;
  const activeFeedbackRef = useRef<AttemptFeedback | null>(activeFeedback);
  activeFeedbackRef.current = activeFeedback;
  const viewOrdinalRef = useRef<number>(viewOrdinal);
  viewOrdinalRef.current = viewOrdinal;
  const endModalVisibleRef = useRef<boolean>(false);
  endModalVisibleRef.current = endModalVisible;
  const historicalNavSeqRef = useRef<number>(0);
  const targetOrdinalRef = useRef<number | null>(null);
  const recoverySeqRef = useRef<number>(0);
  const recoveredSessionIdRef = useRef<string | null>(null);

  // Cancel any active auto-advance countdown
  const cancelAutoAdvance = useCallback((_reason?: string) => {
    if (autoAdvanceTimerRef.current) {
      clearTimeout(autoAdvanceTimerRef.current);
      autoAdvanceTimerRef.current = null;
      setTimerCancelled(true);
    }
  }, []);

  // Update auto-advance switch
  const handleSetAutoAdvanceEnabled = useCallback((enabled: boolean) => {
    setAutoAdvanceEnabled(enabled);
    try {
      localStorage.setItem(AUTO_ADVANCE_STORAGE_KEY, enabled ? "true" : "false");
    } catch {
      // ignore
    }
    if (!enabled) {
      cancelAutoAdvance("disabled_by_user");
    }
  }, [cancelAutoAdvance]);

  // Refresh outbox status
  const refreshOutboxStatus = useCallback(async (currentSessionId?: string) => {
    try {
      const writes = await getPendingWrites(currentSessionId);
      const pending = writes.filter((w) => w.state === "queued" || w.state === "sending");
      const unknown = writes.some((w) => w.state === "unknown");
      setPendingWritesCount(pending.length);
      setHasUnknownWrite(unknown);
      setIsStorageAvailable(true);
    } catch {
      setIsStorageAvailable(false);
    }
  }, []);

  // Load a historical question at specific ordinal
  const loadHistoricalQuestion = useCallback(async (ord: number) => {
    const curSession = sessionRef.current;
    if (!curSession) return;
    const navSeq = ++historicalNavSeqRef.current;
    targetOrdinalRef.current = ord;
    cancelAutoAdvance("navigated_away");

    // Check memory cache
    if (answeredAnswersCache.current.has(ord)) {
      const cached = answeredAnswersCache.current.get(ord)!;
      setViewOrdinal(ord);
      viewOrdinalRef.current = ord;
      setViewAnswer(cached);
      setUiStatus("reviewing");
      return;
    }

    try {
      const ans = await getAnsweredQuestion(curSession.id, ord);
      // Discard if user navigated to another question in the meantime
      if (historicalNavSeqRef.current !== navSeq || targetOrdinalRef.current !== ord) {
        return;
      }
      answeredAnswersCache.current.set(ord, ans);
      setViewOrdinal(ord);
      viewOrdinalRef.current = ord;
      setViewAnswer(ans);
      setUiStatus("reviewing");

      // Register feedback view audit event
      const viewReq: FeedbackViewRequest = {
        client_event_id: getSecureRandomUUID(),
        instance_id: ans.question.instance_id,
        presentation: "history",
        client_displayed_at: new Date().toISOString(),
      };
      try {
        await enqueueWrite({
          schema_version: 1,
          operation_id: viewReq.client_event_id,
          kind: "feedback_view",
          session_id: curSession.id,
          method: "POST",
          path: `/api/v1/practice-sessions/${curSession.id}/feedback-views`,
          idempotency_key: viewReq.client_event_id,
          body_text: JSON.stringify(viewReq),
          state: "queued",
          created_at: viewReq.client_displayed_at,
        });
        await submitFeedbackView(curSession.id, viewReq);
        await updateWriteState(viewReq.client_event_id, "confirmed");
      } catch {
        // Audit is best-effort in background
      }
    } catch (err: any) {
      message.error(err?.message || `无法读取第 ${ord + 1} 题历史`);
    }
  }, [cancelAutoAdvance]);

  // Advance to next question (called either automatically after 500ms or manually)
  const advance = useCallback(async (feedbackOverride?: AttemptFeedback) => {
    cancelAutoAdvance("advance_called");
    historicalNavSeqRef.current++;
    targetOrdinalRef.current = null;

    const curActiveQuestion = activeQuestionRef.current;
    const curActiveFeedback = feedbackOverride || activeFeedbackRef.current;
    const curSession = sessionRef.current;
    const curViewOrdinal = viewOrdinalRef.current;

    // If currently reviewing historical questions
    if (curActiveQuestion && curViewOrdinal < curActiveQuestion.ordinal) {
      const nextOrdinal = curViewOrdinal + 1;
      setViewOrdinal(nextOrdinal);
      viewOrdinalRef.current = nextOrdinal;
      if (nextOrdinal === curActiveQuestion.ordinal) {
        // Returned to active question
        setViewAnswer(null);
        if (curActiveFeedback) {
          setUiStatus(curActiveFeedback.is_correct ? "correct-feedback" : "incorrect-feedback");
        } else {
          setUiStatus("unanswered");
        }
      } else {
        // Load next historical answer
        loadHistoricalQuestion(nextOrdinal);
      }
      return;
    }

    // Currently on active question
    if (!curSession || !curActiveQuestion) return;

    if (advancingLockRef.current) return;

    // Prevent advancing if active question is unanswered
    if (!curActiveFeedback) {
      message.info("请先完成当前题");
      return;
    }

    // Proceed to fetch next question from server
    advancingLockRef.current = true;
    setUiStatus("loading-next");
    try {
      const expectedCursor = curActiveFeedback.next_cursor;
      const res = await fetchNextQuestion(curSession.id, expectedCursor);
      if (res.status === "ready" && res.question) {
        setActiveQuestion(res.question);
        activeQuestionRef.current = res.question;
        setViewOrdinal(res.question.ordinal);
        viewOrdinalRef.current = res.question.ordinal;
        setViewAnswer(null);
        setActiveFeedback(null);
        activeFeedbackRef.current = null;
        setSelectedOptionId(null);
        setTimerCancelled(false);
        submittingLockRef.current = false;
        setUiStatus("unanswered");
      } else if (res.status === "completed") {
        setUiStatus("no-candidates");
        setStatusMessage(res.reason || "当前题库暂无可作答题目");
      } else if (res.status === "unavailable") {
        setUiStatus("no-candidates");
        setStatusMessage(res.reason || "当前题库暂无符合条件的候选题目");
      } else {
        setUiStatus("no-candidates");
        setStatusMessage((res as any)?.reason || "当前题库暂无更多题目");
      }
    } catch (err: any) {
      message.error(err?.message || "加载下一题失败");
      // Keep feedback visible so user can retry
      setUiStatus(curActiveFeedback.is_correct ? "correct-feedback" : "incorrect-feedback");
    } finally {
      advancingLockRef.current = false;
    }
  }, [cancelAutoAdvance, loadHistoricalQuestion]);

  // Recover or initialize session
  const recoverSession = useCallback(async (sid: string) => {
    const recoverySeq = ++recoverySeqRef.current;
    if (recoveredSessionIdRef.current !== sid) {
      recoveredSessionIdRef.current = sid;
      answeredAnswersCache.current.clear();
      lastPendingWriteRef.current = null;
      setRecords([]);
      setViewAnswer(null);
      setActiveFeedback(null);
      setSelectedOptionId(null);
    }
    setUiStatus("loading");
    const isCurrent = () => mountedRef.current && recoverySeqRef.current === recoverySeq;
    try {
      // 1. Check outbox pending writes for unknown state
      const writes = await getPendingWrites(sid).catch(() => []);
      const unknownAttempt = writes.find((w) => w.kind === "practice_attempt" && (w.state === "unknown" || w.state === "sending"));

      // 2. Fetch session detail
      const detail = await getPracticeSession(sid);
      if (!isCurrent()) return;
      setSession(detail.session);
      setSummary(detail.summary);
      let allRecords = [...(detail.recent_records || [])];
      if (detail.has_older_records && allRecords.length > 0) {
        let beforeOrdinal = Math.min(...allRecords.map((r) => r.ordinal));
        let hasMore = true;
        const visitedCursors = new Set<number>();
        while (hasMore && beforeOrdinal > 0 && !visitedCursors.has(beforeOrdinal)) {
          visitedCursors.add(beforeOrdinal);
          try {
            const older = await getAnswerRecords(sid, beforeOrdinal, 100);
            if (!isCurrent()) return;
            if (older.items && older.items.length > 0) {
              const existingOrdinals = new Set(allRecords.map((r) => r.ordinal));
              for (const it of older.items) {
                if (!existingOrdinals.has(it.ordinal)) {
                  allRecords.push(it);
                  existingOrdinals.add(it.ordinal);
                }
              }
              const nextBefore = older.next_before_ordinal;
              hasMore = Boolean(older.has_more && nextBefore !== null && nextBefore >= 0);
              if (!hasMore || nextBefore === null || nextBefore >= beforeOrdinal) {
                hasMore = false;
              } else {
                beforeOrdinal = nextBefore;
              }
            } else {
              hasMore = false;
            }
          } catch {
            hasMore = false;
          }
        }
      }
      allRecords.sort((a, b) => a.ordinal - b.ordinal);
      setRecords(allRecords);

      if (detail.session.status === "ended") {
        setUiStatus("ended");
        return;
      }

      // Check if last_answered_question satisfies the unknown attempt
      if (unknownAttempt) {
        let unknownPayload: { instance_id?: string; option_id?: string } | null = null;
        try {
          unknownPayload = JSON.parse(unknownAttempt.body_text);
        } catch {
          await updateWriteState(unknownAttempt.operation_id, "rejected").catch(() => {});
        }
        if (unknownPayload && detail.last_answered_question &&
            detail.last_answered_question.question.instance_id === unknownPayload.instance_id) {
          await updateWriteState(unknownAttempt.operation_id, "confirmed").catch(() => {});
          lastPendingWriteRef.current = null;
        } else if (unknownPayload?.instance_id && unknownPayload?.option_id) {
          // Preserve reference so handleRetryUnknown can use it
          lastPendingWriteRef.current = unknownAttempt;
        }
      }

      // Cache recent records in memory
      if (detail.last_answered_question) {
        answeredAnswersCache.current.set(
          detail.last_answered_question.question.ordinal,
          detail.last_answered_question
        );
      }

      if (detail.current_question) {
        // Has unanswered current question
        setActiveQuestion(detail.current_question);
        setViewOrdinal(detail.current_question.ordinal);
        setViewAnswer(null);
        setActiveFeedback(null);
        setSelectedOptionId(null);
        submittingLockRef.current = false;
        setUiStatus("unanswered");
      } else if (detail.last_answered_question) {
        // Answered but not yet nexted -> restore feedback page (NO auto advance!)
        const lastAns = detail.last_answered_question;
        setActiveQuestion(lastAns.question);
        setViewOrdinal(lastAns.question.ordinal);
        setViewAnswer(null);
        setActiveFeedback(lastAns.feedback);
        setSelectedOptionId(lastAns.selected_option_id);
        submittingLockRef.current = true;
        setTimerCancelled(true); // Must remain held, never auto advance on restore!
        setUiStatus(lastAns.feedback.is_correct ? "correct-feedback" : "incorrect-feedback");
      } else if (detail.session.cursor === 0) {
        // Brand new session: call next to get first question
        const nxt = await fetchNextQuestion(detail.session.id, 0);
        if (!isCurrent()) return;
        if (nxt.status === "ready" && nxt.question) {
          setActiveQuestion(nxt.question);
          setViewOrdinal(nxt.question.ordinal);
          setViewAnswer(null);
          setActiveFeedback(null);
          setSelectedOptionId(null);
          submittingLockRef.current = false;
          setUiStatus("unanswered");
        } else {
          setUiStatus("no-candidates");
          setStatusMessage(nxt.reason || "当前题库暂无符合条件的候选题目");
        }
      } else {
        setUiStatus("no-candidates");
      }

      await refreshOutboxStatus(sid);
    } catch (err: any) {
      setUiStatus("no-candidates");
      setStatusMessage(err?.message || "无法恢复练习会话");
    }
  }, [refreshOutboxStatus]);

  // Submit attempt for the current unanswered question
  const handleSelectOption = useCallback(async (optionId: string) => {
    if (submittingLockRef.current || uiStatus !== "unanswered" || !activeQuestion || !session) {
      return;
    }

    // Obtain synchronous mutex lock immediately to prevent double clicks / multiple keys
    submittingLockRef.current = true;
    setSelectedOptionId(optionId);
    setUiStatus("submitting");
    cancelAutoAdvance("new_submission");

    const operationId = getSecureRandomUUID();
    const pendingItem: PendingWrite = {
      schema_version: 1,
      operation_id: operationId,
      kind: "practice_attempt",
      session_id: session.id,
      method: "POST",
      path: `/api/v1/practice-sessions/${session.id}/attempts`,
      idempotency_key: operationId,
      body_text: JSON.stringify({
        instance_id: activeQuestion.instance_id,
        option_id: optionId,
      }),
      state: "sending",
      created_at: new Date().toISOString(),
    };
    lastPendingWriteRef.current = pendingItem;

    try {
      await enqueueWrite(pendingItem);
    } catch {
      setIsStorageAvailable(false);
    }

    try {
      const fb = await submitAttempt(
        session.id,
        activeQuestion.instance_id,
        optionId,
        operationId
      );

      // Confirm in outbox
      try {
        await updateWriteState(operationId, "confirmed");
        await refreshOutboxStatus(session.id);
      } catch {
        // ignore outbox error
      }

      setActiveFeedback(fb);

      // Update session revision and cursor
      setSession((prev) =>
        prev
          ? {
              ...prev,
              cursor: fb.next_cursor ?? prev.cursor + 1,
              revision: fb.session_revision ?? prev.revision + 1,
            }
          : null
      );

      // Update answer record & summary
      const newRecord: AnswerRecord = {
        instance_id: activeQuestion.instance_id,
        ordinal: activeQuestion.ordinal,
        correct: fb.is_correct,
      };
      setRecords((prev) => [...prev.filter((r) => r.ordinal !== activeQuestion.ordinal), newRecord]);

      setSummary((prev) => ({
        ...prev,
        answered_count: prev.answered_count + 1,
        correct_count: prev.correct_count + (fb.is_correct ? 1 : 0),
        incorrect_count: prev.incorrect_count + (fb.is_correct ? 0 : 1),
      }));

      // Cache as accepted answer
      const accepted: AcceptedAnswer = {
        question: activeQuestion,
        selected_option_id: optionId,
        feedback: fb,
      };
      answeredAnswersCache.current.set(activeQuestion.ordinal, accepted);

      if (fb.is_correct) {
        setUiStatus("correct-feedback");
        setTimerCancelled(false);
        // Start 500ms countdown if auto advance is enabled
        if (autoAdvanceEnabled) {
          autoAdvanceTimerRef.current = setTimeout(() => {
            advance(fb);
          }, 500);
        }
      } else {
        // Incorrect feedback: stays indefinitely
        setUiStatus("incorrect-feedback");
      }
    } catch (err: any) {
      if (err?.code === "IDEMPOTENCY_CONFLICT" || err?.code === "ALREADY_ANSWERED") {
        // Recover from server state
        recoverSession(session.id);
        return;
      }

      // Network / unknown failure
      try {
        await updateWriteState(operationId, "unknown");
        await refreshOutboxStatus(session.id);
      } catch {}

      setUiStatus("submit-unknown");
      setStatusMessage("作答提交结果待确认，请勿重复作答");
    }
  }, [uiStatus, activeQuestion, session, cancelAutoAdvance, autoAdvanceEnabled, advance, refreshOutboxStatus]);

  // Retry confirming an unknown write
  const handleRetryUnknown = useCallback(async () => {
    if (!session || !lastPendingWriteRef.current) return;
    const write = lastPendingWriteRef.current;
    setUiStatus("submitting");
    try {
      const payload = JSON.parse(write.body_text) as { instance_id?: string; option_id?: string };
      if (!payload.instance_id || !payload.option_id) {
        throw new Error("待确认作答记录缺少题目或选项信息");
      }
      await submitAttempt(
        session.id,
        payload.instance_id,
        payload.option_id,
        write.idempotency_key
      );
      await updateWriteState(write.operation_id, "confirmed");
      await refreshOutboxStatus(session.id);
      lastPendingWriteRef.current = null;
      // Re-read the authoritative session so revision, cursor, summary and the
      // displayed question all converge together after an idempotent replay.
      await recoverSession(session.id);
    } catch (err: any) {
      if (err instanceof SyntaxError || err?.message?.includes("缺少题目或选项信息")) {
        await updateWriteState(write.operation_id, "rejected").catch(() => {});
        lastPendingWriteRef.current = null;
        await refreshOutboxStatus(session.id);
        message.error("本地待确认作答记录已损坏，已从重试队列隔离");
      }
      recoverSession(session.id);
    }
  }, [session, refreshOutboxStatus, recoverSession]);

  // Navigation handlers
  const handlePrevious = useCallback(() => {
    if (viewOrdinal > 0) {
      loadHistoricalQuestion(viewOrdinal - 1);
    }
  }, [viewOrdinal, loadHistoricalQuestion]);

  const handleNext = useCallback(() => {
    advance();
  }, [advance]);

  const handleReturnToCurrent = useCallback(() => {
    historicalNavSeqRef.current++;
    targetOrdinalRef.current = null;
    if (!activeQuestion) return;
    cancelAutoAdvance("return_to_current");
    setViewOrdinal(activeQuestion.ordinal);
    setViewAnswer(null);
    if (activeFeedback) {
      setUiStatus(activeFeedback.is_correct ? "correct-feedback" : "incorrect-feedback");
    } else {
      setUiStatus("unanswered");
    }
  }, [activeQuestion, activeFeedback, cancelAutoAdvance]);

  // End session
  const handleConfirmEnd = useCallback(async () => {
    if (!session) return;
    cancelAutoAdvance("end_session");
    try {
      await endPracticeSession(session.id, session.revision ?? 0, "ended");
      setSession((prev) => (prev ? { ...prev, status: "ended" } : null));
      setUiStatus("ended");
      setEndModalVisible(false);
    } catch (err: any) {
      message.error(err?.message || "结束练习失败");
    }
  }, [session, cancelAutoAdvance]);

  // Track the component lifetime independently from route/query changes.
  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      recoverySeqRef.current++;
    };
  }, []);

  // Initial load
  useEffect(() => {
    let unmounted = false;

    async function init() {
      setUiStatus("loading");
      try {
        const ctx = await fetchClientContext().catch(() => ({ csrf_token: "", active_sessions: [] }));

        // Check if sessionIdFromUrl was provided
        if (sessionIdFromUrl) {
          await recoverSession(sessionIdFromUrl);
          return;
        }

        // Determine bankId
        let targetBankId = bankIdFromUrl;
        if (!targetBankId) {
          // Check for active session in context
          const activePractice = (ctx.active_sessions || []).find((s: any) => s.mode === "practice");
          if (activePractice) {
            await recoverSession(activePractice.id);
            return;
          }

          // Fetch banks to find a default
          const banksRes = await getAvailableBanks().catch(() => ({ items: [] }));
          if (banksRes.items && banksRes.items.length > 0) {
            targetBankId = banksRes.items[0].id;
            setAvailableBanks(banksRes.items);
          } else {
            // Default core seed bank
            targetBankId = "00000000-0000-4000-8000-000000000801";
          }
        }

        // Create new session or handle active conflict
        try {
          const createRes = await createPracticeSession(targetBankId);
          if (unmounted) return;
          const sid = createRes.session.id;
          await recoverSession(sid);
        } catch (err: any) {
          if (unmounted) return;
          if (err?.code === "ACTIVE_SESSION_EXISTS" || err?.code === "SESSION_ACTIVE") {
            setConflictSessionId(err.details?.session_id || err.details?.active_session_id || null);
            setUiStatus("active-conflict");
            return;
          }
          throw err;
        }
      } catch (err: any) {
        if (unmounted) return;
        setUiStatus("no-candidates");
        setStatusMessage(err?.message || "无法创建练习会话");
      }
    }

    init();

    return () => {
      unmounted = true;
      recoverySeqRef.current++;
      if (autoAdvanceTimerRef.current) {
        clearTimeout(autoAdvanceTimerRef.current);
      }
    };
  }, [bankIdFromUrl, sessionIdFromUrl, restartToken, recoverSession]);

  // Handle visibility change to cancel auto-advance
  useEffect(() => {
    const handleVisibilityChange = () => {
      if (document.hidden) {
        cancelAutoAdvance("visibility_hidden");
      }
    };
    document.addEventListener("visibilitychange", handleVisibilityChange);
    return () => document.removeEventListener("visibilitychange", handleVisibilityChange);
  }, [cancelAutoAdvance]);

  // Keyboard navigation & answering (PR-16, PR-17)
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      // Ignore when modal is open (except Escape to close it)
      if (endModalVisibleRef.current) {
        if (e.key === "Escape") {
          cancelAutoAdvance("escape_key");
          setEndModalVisible(false);
        }
        return;
      }

      // Ignore in inputs, textareas or modals
      const tag = (e.target as HTMLElement)?.tagName;
      if (["INPUT", "TEXTAREA", "SELECT"].includes(tag) || (e.target as HTMLElement)?.isContentEditable) {
        return;
      }
      if (e.repeat || e.ctrlKey || e.metaKey || e.altKey) {
        return;
      }

      // 1-4 for answering
      if (["1", "2", "3", "4"].includes(e.key)) {
        if (uiStatus === "unanswered" && activeQuestion && viewOrdinal === activeQuestion.ordinal) {
          const idx = parseInt(e.key, 10) - 1;
          if (idx >= 0 && idx < activeQuestion.options.length) {
            handleSelectOption(activeQuestion.options[idx].id);
          }
        }
        return;
      }

      // A / a for Previous
      if (e.key === "a" || e.key === "A") {
        handlePrevious();
        return;
      }

      // D / d for Next
      if (e.key === "d" || e.key === "D") {
        handleNext();
        return;
      }

      // Enter for Next or retry unknown
      if (e.key === "Enter") {
        if (uiStatus === "correct-feedback" || uiStatus === "incorrect-feedback") {
          handleNext();
        } else if (uiStatus === "submit-unknown") {
          handleRetryUnknown();
        }
        return;
      }

      // Escape to cancel timer or close modals
      if (e.key === "Escape") {
        cancelAutoAdvance("escape_key");
        setEndModalVisible(false);
        return;
      }
    };

    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [uiStatus, activeQuestion, viewOrdinal, handleSelectOption, handlePrevious, handleNext, handleRetryUnknown, cancelAutoAdvance]);

  return {
    session,
    activeQuestion,
    activeFeedback,
    selectedOptionId,
    viewOrdinal,
    viewAnswer,
    records,
    summary,
    uiStatus,
    statusMessage,
    pendingWritesCount,
    hasUnknownWrite,
    isStorageAvailable,
    autoAdvanceEnabled,
    timerCancelled,
    endModalVisible,
    conflictSessionId,
    availableBanks,
    setEndModalVisible,
    setAutoAdvanceEnabled: handleSetAutoAdvanceEnabled,
    cancelAutoAdvance,
    handleSelectOption,
    handlePrevious,
    handleNext,
    handleReturnToCurrent,
    loadHistoricalQuestion,
    handleConfirmEnd,
    handleRetryUnknown,
    recoverSession,
  };
}
