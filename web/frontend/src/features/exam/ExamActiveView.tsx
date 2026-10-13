import React from "react";
import {
  Card,
  Button,
  Space,
  Typography,
  Progress,
  Modal,
  Alert,
  Spin,
  Tag,
} from "antd";
import {
  ClockCircleOutlined,
  LeftOutlined,
  RightOutlined,
  SendOutlined,
  StopOutlined,
  SyncOutlined,
} from "@ant-design/icons";
import { useExamSession } from "./useExamSession";
import { QuestionStem, taskFamilyLabels } from "../../components/QuestionStem";
import { ExamOptions } from "../../components/ExamOptions";
import { AnswerSheet, AnswerRecordItem } from "../../components/AnswerSheet";
import { SessionSyncStatus } from "../../components/SessionSyncStatus";

const { Title, Text, Paragraph } = Typography;

interface ExamActiveViewProps {
  examId: string;
  onNavigate?: (path: string) => void;
}

export const ExamActiveView: React.FC<ExamActiveViewProps> = ({
  examId,
  onNavigate,
}) => {
  const {
    exam,
    loading,
    error,
    currentQuestions,
    currentQuestion,
    viewOrdinal,
    selectedOptionId,
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
    drafts,
  } = useExamSession({ examId, onNavigate });

  if (loading) {
    return (
      <div style={{ textAlign: "center", padding: "64px 0" }} data-testid="exam-loading">
        <Spin size="large" tip="正在准备试卷..." />
      </div>
    );
  }

  if (error || !exam) {
    return (
      <div style={{ maxWidth: 600, margin: "32px auto", padding: "0 16px" }}>
        <Alert
          type="error"
          showIcon
          message="无法加载试卷"
          description={error || "考试不存在或已作废"}
          action={
            <Button size="small" onClick={() => onNavigate?.("/exams/new")}>
              返回创建
            </Button>
          }
          data-testid="exam-error-alert"
        />
      </div>
    );
  }

  // Format countdown mm:ss
  let timerText: string | null = null;
  let isUrgent = false;
  if (remainingSeconds !== null) {
    const mins = Math.floor(remainingSeconds / 60);
    const secs = remainingSeconds % 60;
    timerText = `${mins.toString().padStart(2, "0")}:${secs.toString().padStart(2, "0")}`;
    if (remainingSeconds < 300) {
      isUrgent = true;
    }
  }

  // Format answer sheet records
  const sheetRecords: AnswerRecordItem[] = currentQuestions.map((q, idx) => {
    const hasAnswer = Boolean(drafts.get(q.instance_id));
    return {
      ordinal: idx,
      instanceId: q.instance_id,
      status: hasAnswer ? "correct" : "unanswered", // in exam mode, AnswerSheet checks status !== "unanswered"
    };
  });

  return (
    <div
      style={{
        maxWidth: 1000,
        margin: "16px auto",
        padding: "0 16px",
      }}
      data-testid="exam-active-view"
    >
      {/* Top Session Bar */}
      <Card
        size="small"
        style={{ marginBottom: 16 }}
        bodyStyle={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          flexWrap: "wrap",
          gap: 12,
        }}
      >
        <Space size={[12, 8]} align="center" wrap style={{ maxWidth: "100%" }}>
          <Title level={5} style={{ margin: 0 }}>
            阶段考试
          </Title>
          <Text type="secondary" data-testid="exam-progress-text">
            已答 {answeredCount} / {totalCount} 题
          </Text>
          <Progress
            percent={totalCount > 0 ? Math.round((answeredCount / totalCount) * 100) : 0}
            size="small"
            style={{ width: 120 }}
            showInfo={false}
          />
        </Space>

        <Space size={[8, 8]} align="center" wrap style={{ maxWidth: "100%" }}>
          {timerText && (
            <Tag
              icon={<ClockCircleOutlined />}
              color={isUrgent ? "error" : "default"}
              style={{ fontSize: 14, padding: "2px 8px" }}
              data-testid="exam-countdown-timer"
            >
              剩余时间 {timerText}
            </Tag>
          )}

          <SessionSyncStatus
            pendingCount={pendingCount}
            isStorageAvailable={isStorageAvailable}
            hasUnknown={hasUnknown}
          />

          {hasUnknown && (
            <Button
              size="small"
              icon={<SyncOutlined />}
              onClick={retrySync}
              data-testid="retry-sync-btn"
            >
              重试同步
            </Button>
          )}

          <Button
            type="primary"
            danger={false}
            icon={<SendOutlined />}
            onClick={requestSubmitModal}
            loading={isSubmitting || deadlineSettling}
            data-testid="submit-exam-btn"
          >
            交卷
          </Button>

          <Button
            type="text"
            danger
            icon={<StopOutlined />}
            onClick={() => setAbandonModalVisible(true)}
            data-testid="abandon-exam-btn"
          >
            放弃
          </Button>
        </Space>
      </Card>

      {/* Sync / Expiry Warning banner */}
      {syncWarning && (
        <Alert
          type="warning"
          showIcon
          message={syncWarning}
          style={{ marginBottom: 16 }}
          data-testid="exam-sync-warning"
        />
      )}

      {isExpired && (
        <Alert
          type="info"
          showIcon
          message="考试已到截止时间，正在自动确认交卷状态"
          style={{ marginBottom: 16 }}
          data-testid="exam-expired-alert"
          action={
            <Button size="small" type="primary" loading={deadlineSettling} onClick={() => handleDeadlineExpire(true)} data-testid="settle-expired-btn">
              立即重试
            </Button>
          }
        />
      )}

      {/* Main Content: Left AnswerSheet, Right Question Stem & Options */}
      <div
        style={{
          display: "flex",
          gap: 20,
          flexDirection: "row",
          flexWrap: "wrap",
        }}
      >
        {/* Left column: Answer Sheet */}
        <div style={{ flex: "0 0 220px", maxWidth: "100%" }}>
          <AnswerSheet
            records={sheetRecords}
            currentOrdinal={viewOrdinal}
            viewOrdinal={viewOrdinal}
            mode="exam"
            onSelectOrdinal={handleJump}
          />
        </div>

        {/* Right column: Question Stem & Options */}
        <Card style={{ flex: "1 1 320px", minWidth: 280 }} data-testid="exam-question-card">
          {currentQuestion ? (
            <Space direction="vertical" style={{ width: "100%" }} size={24}>
              <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", flexWrap: "wrap", gap: 8 }}>
                <Text type="secondary" style={{ fontSize: 13, minWidth: 0 }}>
                  第 {viewOrdinal + 1} 题 / 共 {totalCount} 题
                </Text>
                <Tag color="blue" style={{ maxWidth: "100%", overflow: "hidden", textOverflow: "ellipsis" }} title={currentQuestion.task_family}>
                  {taskFamilyLabels[currentQuestion.task_family] || currentQuestion.task_family}
                </Tag>
              </div>

              <QuestionStem stem={currentQuestion.stem} />

              <ExamOptions
                options={currentQuestion.options}
                selectedOptionId={selectedOptionId}
                disabled={isExpired || isSubmitting || submitModalVisible || abandonModalVisible}
                onSelect={(optId) => selectOption(currentQuestion.instance_id, optId)}
                onClear={() => clearSelection(currentQuestion.instance_id)}
              />

              {/* Navigation controls */}
              <div
                style={{
                  display: "flex",
                  flexDirection: "column",
                  gap: 12,
                  paddingTop: 16,
                  borderTop: "1px solid #f0f0f0",
                }}
              >
                <div
                  style={{
                    display: "flex",
                    justifyContent: "space-between",
                    alignItems: "center",
                  }}
                >
                  <Button
                    icon={<LeftOutlined />}
                    onClick={handlePrev}
                    disabled={viewOrdinal === 0 || isSubmitting || submitModalVisible || abandonModalVisible}
                    data-testid="prev-question-btn"
                  >
                    上一题 (A)
                  </Button>

                  <Button
                    onClick={handleNext}
                    disabled={viewOrdinal >= totalCount - 1 || isSubmitting || submitModalVisible || abandonModalVisible}
                    data-testid="next-question-btn"
                  >
                    下一题 (D)
                    <RightOutlined />
                  </Button>
                </div>

                <div style={{ textAlign: "center" }} data-testid="exam-shortcut-hint">
                  <Text type="secondary" style={{ fontSize: 12 }}>
                    快捷键: 1–4 选择 · A/D 切换
                  </Text>
                </div>
              </div>
            </Space>
          ) : (
            <div style={{ textAlign: "center", padding: "32px 0" }}>
              <Text type="secondary">本试卷没有题目</Text>
            </div>
          )}
        </Card>
      </div>

      {/* Submit Confirmation Modal */}
      <Modal
        title="确认交卷"
        open={submitModalVisible}
        onOk={confirmSubmit}
        onCancel={() => setSubmitModalVisible(false)}
        okText="确认交卷"
        cancelText="返回检查"
        confirmLoading={isSubmitting}
      >
        <div data-testid="submit-confirm-modal">
          <Space direction="vertical" size={12} style={{ width: "100%", padding: "12px 0" }}>
            <Text>
              共 <strong>{totalCount}</strong> 题，已答 <strong>{answeredCount}</strong> 题，未答{" "}
              <strong>{unansweredCount}</strong> 题。
            </Text>
            <Text type="secondary">
              确认交卷后，系统将锁定作答并计算成绩，生成包含改答与首次选择的完整分析报告。
            </Text>
          </Space>
        </div>
      </Modal>

      {/* Abandon Confirmation Modal */}
      <Modal
        title="放弃考试"
        open={abandonModalVisible}
        onOk={confirmAbandon}
        onCancel={() => setAbandonModalVisible(false)}
        okText="确认放弃"
        cancelText="取消"
        okButtonProps={{ danger: true }}
      >
        <div data-testid="abandon-confirm-modal">
          <Paragraph>
            确定要放弃本次考试吗？放弃后将以当前状态交卷或退出，无法撤销。
          </Paragraph>
        </div>
      </Modal>
    </div>
  );
};
