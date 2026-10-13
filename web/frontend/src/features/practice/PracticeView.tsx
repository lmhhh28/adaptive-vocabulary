import React from "react";
import {
  Breadcrumb,
  Card,
  Button,
  Space,
  Typography,
  Switch,
  Modal,
  Alert,
  Skeleton,
  Result,
  Row,
  Col,
} from "antd";
import {
  ArrowLeftOutlined,
  ArrowRightOutlined,
  StopOutlined,
  HomeOutlined,
  PauseCircleOutlined,
} from "@ant-design/icons";
import { usePracticeSession } from "./usePracticeSession";
import { QuestionStem } from "../../components/QuestionStem";
import { PracticeOptions } from "../../components/PracticeOptions";
import { FeedbackPanel } from "../../components/FeedbackPanel";
import { AnswerSheet, AnswerRecordItem } from "../../components/AnswerSheet";
import { SessionSyncStatus } from "../../components/SessionSyncStatus";

const { Title, Text, Paragraph } = Typography;

export interface PracticeViewProps {
  onNavigate?: (path: string) => void;
}

export const PracticeView: React.FC<PracticeViewProps> = ({ onNavigate }) => {
  const urlParams = new URLSearchParams(window.location.search);
  const bankIdFromUrl = urlParams.get("bank_id") || undefined;
  const sessionIdFromUrl = urlParams.get("session_id") || undefined;
  const restartToken = urlParams.get("restart") || undefined;

  const {
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
    setEndModalVisible,
    setAutoAdvanceEnabled,
    cancelAutoAdvance,
    handleSelectOption,
    handlePrevious,
    handleNext,
    handleReturnToCurrent,
    loadHistoricalQuestion,
    handleConfirmEnd,
    handleRetryUnknown,
    recoverSession,
  } = usePracticeSession(bankIdFromUrl, sessionIdFromUrl, restartToken);

  const isReviewing = Boolean(viewAnswer);
  const displayedQuestion = isReviewing ? viewAnswer?.question : activeQuestion;
  const displayedFeedback = isReviewing ? viewAnswer?.feedback : activeFeedback;

  let displayedSelectedOption = selectedOptionId;
  let displayedCorrectOption = displayedFeedback?.correct_option_id;
  if (isReviewing && viewAnswer) {
    displayedSelectedOption = viewAnswer.selected_option_id;
  }

  // Determine options component status
  let optionsComponentStatus: any = "unanswered";
  if (isReviewing) {
    optionsComponentStatus = "reviewing";
  } else if (uiStatus === "submitting") {
    optionsComponentStatus = "submitting";
  } else if (uiStatus === "submit-unknown") {
    optionsComponentStatus = "submit-unknown";
  } else if (uiStatus === "correct-feedback") {
    optionsComponentStatus = "correct-feedback";
  } else if (uiStatus === "incorrect-feedback") {
    optionsComponentStatus = "incorrect-feedback";
  }

  // Answer text lookup
  let userAnswerText: string | undefined;
  let correctAnswerText: string | undefined;
  if (displayedQuestion && displayedFeedback) {
    userAnswerText = displayedQuestion.options.find((o) => o.id === displayedSelectedOption)?.text;
    correctAnswerText = displayedQuestion.options.find((o) => o.id === displayedCorrectOption)?.text;
  }

  // Answer sheet items conversion
  const sheetItems: AnswerRecordItem[] = records.map((r) => ({
    ordinal: r.ordinal,
    instanceId: r.instance_id,
    status: r.correct ? ("correct" as const) : ("incorrect" as const),
  }));

  // If current question is active and not yet answered, add it to the sheet
  if (activeQuestion && !sheetItems.some((item) => item.ordinal === activeQuestion.ordinal)) {
    sheetItems.push({
      ordinal: activeQuestion.ordinal,
      instanceId: activeQuestion.instance_id,
      status: "unanswered",
    });
  }
  sheetItems.sort((a, b) => a.ordinal - b.ordinal);

  return (
    <div style={{ maxWidth: 1200, margin: "0 auto", padding: "20px 0" }} data-testid="practice-view">
      {/* Breadcrumb and Header */}
      <div style={{ marginBottom: 16, display: "flex", justifyContent: "space-between", alignItems: "center" }}>
        <Breadcrumb
          items={[
            { title: <span style={{ cursor: "pointer" }} onClick={() => onNavigate?.("/")}><HomeOutlined /> 首页</span> },
            { title: "自适应练习" },
          ]}
        />
        <Space>
          <SessionSyncStatus
            pendingCount={pendingWritesCount}
            isStorageAvailable={isStorageAvailable}
            hasUnknown={hasUnknownWrite}
          />
          {session && uiStatus !== "ended" && (
            <Button
              danger
              size="small"
              icon={<StopOutlined />}
              onClick={() => {
                cancelAutoAdvance("end_modal_opened");
                setEndModalVisible(true);
              }}
              data-testid="end-session-btn"
            >
              结束练习
            </Button>
          )}
        </Space>
      </div>

      {/* Active Conflict State */}
      {uiStatus === "active-conflict" && (
        <Alert
          type="warning"
          showIcon
          message="存在进行中的作答会话"
          description={
            <div>
              <Paragraph style={{ marginTop: 8 }}>
                系统检测到另一个正在进行中的练习会话（ID: {conflictSessionId}）。为保护学习记录一致性，不可并发作答。
              </Paragraph>
              <Space>
                {conflictSessionId && (
                  <Button
                    type="primary"
                    onClick={() => recoverSession(conflictSessionId)}
                    data-testid="resume-conflict-btn"
                  >
                    继续当前会话
                  </Button>
                )}
                <Button onClick={() => onNavigate?.("/")}>返回首页</Button>
              </Space>
            </div>
          }
          style={{ margin: "40px auto", maxWidth: 720 }}
          data-testid="active-conflict-alert"
        />
      )}

      {/* Loading State */}
      {uiStatus === "loading" && (
        <Card style={{ maxWidth: 840, margin: "40px auto", padding: 24 }}>
          <Skeleton active paragraph={{ rows: 6 }} />
        </Card>
      )}

      {/* No Candidates State */}
      {uiStatus === "no-candidates" && (
        <Card style={{ maxWidth: 840, margin: "40px auto", textAlign: "center", padding: 32 }} data-testid="no-candidates-alert">
          <Result
            status="info"
            title="本轮练习暂无可作答题目"
            subTitle={statusMessage || "题库中候选题目均处于记忆冷却期或无符合条件的内容。"}
            extra={[
              <Button
                type="primary"
                key="end"
                onClick={() => {
                  cancelAutoAdvance("end_modal_opened");
                  setEndModalVisible(true);
                }}
                data-testid="no-candidates-end-btn"
              >
                结算本轮练习
              </Button>,
              viewOrdinal > 0 && (
                <Button key="review" onClick={() => loadHistoricalQuestion(viewOrdinal - 1)}>
                  回看上一题
                </Button>
              ),
              <Button key="home" onClick={() => onNavigate?.("/")}>
                返回首页
              </Button>,
            ]}
          />
        </Card>
      )}

      {/* Ended Summary State */}
      {uiStatus === "ended" && (
        <Card style={{ maxWidth: 840, margin: "40px auto", padding: 32 }} data-testid="practice-ended-summary">
          <Result
            status="success"
            title="练习已结束"
            subTitle="已完成的作答已完整保存到学习记录中，未作答题目不计入结果。"
            extra={[
              <Button type="primary" key="home" onClick={() => onNavigate?.("/")}>
                返回首页
              </Button>,
              <Button
                key="restart"
                onClick={() => {
                  const restart = Date.now().toString(36);
                  const targetPath = session?.bank_id
                    ? `/practice?bank_id=${session.bank_id}&restart=${restart}`
                    : `/practice?restart=${restart}`;
                  if (onNavigate) {
                    onNavigate(targetPath);
                  } else {
                    window.location.href = targetPath;
                  }
                }}
                data-testid="restart-practice-btn"
              >
                再练一轮
              </Button>,
            ]}
          >
            <div style={{ backgroundColor: "#fafafa", padding: 20, borderRadius: 4, marginTop: 16 }}>
              <Row gutter={24} style={{ textAlign: "center" }}>
                <Col span={8}>
                  <Text type="secondary">作答总题数</Text>
                  <Title level={3} style={{ margin: "4px 0" }}>{summary.answered_count}</Title>
                </Col>
                <Col span={8}>
                  <Text type="secondary">答对题数</Text>
                  <Title level={3} style={{ margin: "4px 0", color: "#52c41a" }}>{summary.correct_count}</Title>
                </Col>
                <Col span={8}>
                  <Text type="secondary">待巩固题数</Text>
                  <Title level={3} style={{ margin: "4px 0", color: "#ff4d4f" }}>{summary.incorrect_count}</Title>
                </Col>
              </Row>
            </div>
          </Result>
        </Card>
      )}

      {/* Main Question Answering & Review View */}
      {uiStatus !== "loading" && uiStatus !== "active-conflict" && uiStatus !== "no-candidates" && uiStatus !== "ended" && displayedQuestion && (
        <Row gutter={24}>
          {/* Left column: Answer Sheet */}
          <Col xs={24} sm={24} md={6} lg={5}>
            <AnswerSheet
              records={sheetItems}
              currentOrdinal={activeQuestion?.ordinal}
              viewOrdinal={viewOrdinal}
              pageSize={50}
              counts={{
                answered: summary.answered_count,
                correct: summary.correct_count,
                incorrect: summary.incorrect_count,
              }}
              onSelectOrdinal={(ord) => {
                if (activeQuestion && ord === activeQuestion.ordinal) {
                  handleReturnToCurrent();
                } else {
                  loadHistoricalQuestion(ord);
                }
              }}
              onReturnToCurrent={handleReturnToCurrent}
            />
          </Col>

          {/* Right column: Question & Options */}
          <Col xs={24} sm={24} md={18} lg={19}>
            <Card
              style={{
                borderRadius: 6,
                minHeight: 460,
                boxShadow: "0 1px 2px rgba(0,0,0,0.03)",
              }}
            >
              {/* Question stem */}
              <QuestionStem
                stem={displayedQuestion.stem}
                taskFamily={displayedQuestion.task_family}
                ordinal={displayedQuestion.ordinal}
              />

              {/* Review badge */}
              {isReviewing && (
                <Alert
                  message="当前处于历史回看模式（只读）。按数字键不作答，点击「回到当前题」继续练习。"
                  type="info"
                  showIcon
                  style={{ marginBottom: 16 }}
                  data-testid="reviewing-alert"
                />
              )}

              {/* Unknown error / retry alert */}
              {uiStatus === "submit-unknown" && (
                <Alert
                  message="作答提交结果待确认"
                  description={
                    <Space direction="vertical" style={{ marginTop: 8 }}>
                      <Text type="secondary">网络响应尚未确认到达。请点击按钮重试确认原作答。</Text>
                      <Button type="primary" size="small" onClick={handleRetryUnknown} data-testid="retry-unknown-btn">
                        重试确认
                      </Button>
                    </Space>
                  }
                  type="warning"
                  showIcon
                  style={{ marginBottom: 16 }}
                />
              )}

              {/* Options */}
              <PracticeOptions
                options={displayedQuestion.options}
                selectedOptionId={displayedSelectedOption || undefined}
                correctOptionId={displayedCorrectOption || undefined}
                status={optionsComponentStatus}
                onSelect={handleSelectOption}
                disabled={isReviewing || endModalVisible || uiStatus === "submitting" || uiStatus === "submit-unknown"}
              />

              {/* Feedback Panel */}
              {displayedFeedback && (
                <div style={{ marginTop: 24 }}>
                  <FeedbackPanel
                    status={displayedFeedback.is_correct ? "correct" : "incorrect"}
                    isCorrect={displayedFeedback.is_correct}
                    userAnswerText={userAnswerText}
                    correctAnswerText={correctAnswerText}
                    explanation={displayedFeedback.explanation}
                    onNext={handleNext}
                    nextLoading={uiStatus === "loading-next"}
                    nextDisabled={uiStatus === "loading-next"}
                    autoAdvanceEnabled={autoAdvanceEnabled && !timerCancelled}
                  />

                  {/* Stay on question button for auto-advance */}
                  {displayedFeedback.is_correct && autoAdvanceEnabled && !timerCancelled && !isReviewing && (
                    <div style={{ marginTop: 8, display: "flex", justifyContent: "flex-end" }}>
                      <Button
                        type="link"
                        size="small"
                        icon={<PauseCircleOutlined />}
                        onClick={() => cancelAutoAdvance("user_clicked_stay")}
                        data-testid="stay-on-question-btn"
                      >
                        留在本题
                      </Button>
                    </div>
                  )}
                </div>
              )}

              {/* Bottom Navigation and Controls */}
              <div
                style={{
                  marginTop: 32,
                  paddingTop: 16,
                  borderTop: "1px solid #f0f0f0",
                  display: "flex",
                  justifyContent: "space-between",
                  alignItems: "center",
                  flexWrap: "wrap",
                  gap: 12,
                }}
              >
                <Space>
                  <Button
                    icon={<ArrowLeftOutlined />}
                    disabled={viewOrdinal <= 0}
                    onClick={handlePrevious}
                    data-testid="prev-question-btn"
                  >
                    上一题 (A)
                  </Button>
                  <Button
                    type="primary"
                    icon={<ArrowRightOutlined />}
                    disabled={uiStatus === "unanswered" || uiStatus === "submitting" || uiStatus === "loading-next"}
                    loading={uiStatus === "loading-next"}
                    onClick={handleNext}
                    data-testid="bottom-next-btn"
                  >
                    下一题 (D)
                  </Button>
                </Space>

                <Space size={16}>
                  <Space size={6}>
                    <Switch
                      size="small"
                      checked={autoAdvanceEnabled}
                      onChange={setAutoAdvanceEnabled}
                      data-testid="auto-advance-switch"
                    />
                    <Text type="secondary" style={{ fontSize: 13 }}>答对自动下一题</Text>
                  </Space>
                  <Text type="secondary" style={{ fontSize: 12, color: "#bfbfbf" }}>
                    1–4 作答 · A 上一题 · D 下一题
                  </Text>
                </Space>
              </div>
            </Card>
          </Col>
        </Row>
      )}

      {/* End Session Confirmation Modal */}
      <Modal
        title="结束本轮练习？"
        open={endModalVisible}
        onOk={handleConfirmEnd}
        onCancel={() => setEndModalVisible(false)}
        okText="结束练习"
        cancelText="继续练习"
        okButtonProps={{ danger: true, "data-testid": "confirm-end-btn" }}
        data-testid="end-session-modal"
      >
        <Paragraph>已完成的作答会保留，尚未作答的题目不计入结果。</Paragraph>
      </Modal>
    </div>
  );
};
