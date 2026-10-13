import React, { useState, useEffect } from "react";
import { Card, Row, Col, Typography, Button, Space, Switch, Divider, message } from "antd";
import { QuestionStem } from "../components/QuestionStem";
import { PracticeOptions } from "../components/PracticeOptions";
import { ExamOptions } from "../components/ExamOptions";
import { FeedbackPanel } from "../components/FeedbackPanel";
import { AnswerSheet } from "../components/AnswerSheet";
import { SessionSyncStatus } from "../components/SessionSyncStatus";
import {
  enqueueWrite,
  getPendingWrites,
  clearAllWrites,
  simulateStorageFault,
  type PendingWrite,
} from "../api/outbox";

const { Title, Paragraph, Text } = Typography;

export const ComponentsDemoView: React.FC = () => {
  const [selectedPracticeOption, setSelectedPracticeOption] = useState<string | undefined>();
  const [practiceStatus, setPracticeStatus] = useState<any>("unanswered");
  const [autoAdvance, setAutoAdvance] = useState(true);

  const [selectedExamOption, setSelectedExamOption] = useState<string | undefined>();

  const [currentOrdinal] = useState(3);
  const [viewOrdinal, setViewOrdinal] = useState(3);

  const [outboxItems, setOutboxItems] = useState<PendingWrite[]>([]);
  const [storageFault, setStorageFault] = useState(false);
  const [outboxError, setOutboxError] = useState<string | null>(null);

  const mockOptions = [
    { id: "opt-1", text: "meticulous - extremely careful and precise" },
    { id: "opt-2", text: "nebulous - vague or ill-defined" },
    { id: "opt-3", text: "ephemeral - lasting for a very short time" },
    { id: "opt-4", text: "ubiquitous - present, appearing everywhere" },
  ];

  const refreshOutbox = async () => {
    try {
      setOutboxError(null);
      const items = await getPendingWrites("demo-session");
      setOutboxItems(items);
    } catch (err: any) {
      setOutboxError(err.message || String(err));
    }
  };

  useEffect(() => {
    refreshOutbox();
  }, []);

  const handlePracticeSelect = (optId: string) => {
    setSelectedPracticeOption(optId);
    setPracticeStatus("submitting");
    setTimeout(() => {
      if (optId === "opt-1") {
        setPracticeStatus("correct-feedback");
      } else {
        setPracticeStatus("incorrect-feedback");
      }
    }, 400);
  };

  const handleEnqueueDemoWrite = async () => {
    try {
      setOutboxError(null);
      const item: PendingWrite = {
        schema_version: 1,
        operation_id: `op-${Date.now()}-${Math.random().toString(36).substring(2, 6)}`,
        kind: "practice_attempt",
        session_id: "demo-session",
        method: "POST",
        path: "/api/v1/practice-sessions/demo-session/attempts",
        idempotency_key: `key-${Date.now()}`,
        body_text: JSON.stringify({ option_id: selectedPracticeOption || "opt-1" }),
        state: "queued",
        created_at: new Date().toISOString(),
      };
      await enqueueWrite(item);
      message.success("已写入 Outbox 队列");
      await refreshOutbox();
    } catch (err: any) {
      setOutboxError(err.message || String(err));
      message.error(`写入失败: ${err.message}`);
    }
  };

  const handleToggleFault = (fault: boolean) => {
    setStorageFault(fault);
    simulateStorageFault(fault);
    refreshOutbox();
  };

  const mockRecords: any[] = Array.from({ length: 45 }, (_, i) => ({
    ordinal: i,
    status: i < 3 ? (i % 2 === 0 ? "correct" : "incorrect") : (i === 3 ? "current" : "unanswered"),
  }));

  return (
    <div style={{ maxWidth: 1100, margin: "0 auto", padding: "16px 0" }} data-testid="components-demo-view">
      <Title level={3}>W00 共享纯展示组件与沙盒</Title>
      <Paragraph type="secondary">
        该沙盒集中承载 W00 交付的纯展示组件、IndexedDB Outbox 持久队列以及 1–4 快捷键响应。
      </Paragraph>

      <Row gutter={[24, 24]}>
        <Col xs={24} md={14}>
          <Card title="自适应练习展示组件" style={{ marginBottom: 24 }}>
            <QuestionStem
              taskFamily="meaning_recognition"
              ordinal={viewOrdinal}
              totalCount={45}
              stem="Which word best matches the definition: meticulous?"
            />

            <PracticeOptions
              options={mockOptions}
              selectedOptionId={selectedPracticeOption}
              correctOptionId="opt-1"
              status={practiceStatus}
              onSelect={handlePracticeSelect}
            />

            {practiceStatus !== "unanswered" && (
              <FeedbackPanel
                status={practiceStatus === "correct-feedback" ? "correct" : "incorrect"}
                isCorrect={practiceStatus === "correct-feedback"}
                userAnswerText={mockOptions.find((o) => o.id === selectedPracticeOption)?.text}
                correctAnswerText={mockOptions[0].text}
                explanation="meticulous 意为一丝不苟的、极仔细的。"
                autoAdvanceEnabled={autoAdvance}
                onNext={() => {
                  setPracticeStatus("unanswered");
                  setSelectedPracticeOption(undefined);
                  setViewOrdinal((v) => v + 1);
                }}
              />
            )}

            <Divider />
            <Space>
              <Button size="small" onClick={() => { setPracticeStatus("unanswered"); setSelectedPracticeOption(undefined); }}>
                重置作答态
              </Button>
              <Space>
                <Text type="secondary">答对自动前进:</Text>
                <Switch size="small" checked={autoAdvance} onChange={setAutoAdvance} />
              </Space>
            </Space>
          </Card>

          <Card title="阶段考试选项组件">
            <ExamOptions
              options={mockOptions}
              selectedOptionId={selectedExamOption}
              onSelect={setSelectedExamOption}
              onClear={() => setSelectedExamOption(undefined)}
            />
          </Card>
        </Col>

        <Col xs={24} md={10}>
          <Card title="答题卡组件 (每页30条)" style={{ marginBottom: 24 }}>
            <AnswerSheet
              records={mockRecords}
              currentOrdinal={currentOrdinal}
              viewOrdinal={viewOrdinal}
              pageSize={30}
              onSelectOrdinal={(ord) => setViewOrdinal(ord)}
              onReturnToCurrent={() => setViewOrdinal(currentOrdinal)}
              counts={{ answered: 3, correct: 2, incorrect: 1 }}
              mode="practice"
            />
          </Card>

          <Card title="IndexedDB Outbox 持久队列与同步状态">
            <div style={{ marginBottom: 16 }}>
              <Space>
                <Text>持久队列状态:</Text>
                <SessionSyncStatus
                  pendingCount={outboxItems.length}
                  isStorageAvailable={!storageFault}
                  hasUnknown={outboxItems.some((i) => i.state === "unknown")}
                />
              </Space>
            </div>

            <div style={{ marginBottom: 16 }}>
              <Space wrap>
                <Button size="small" type="primary" onClick={handleEnqueueDemoWrite} data-testid="enqueue-outbox-btn">
                  追加作答事件
                </Button>
                <Button size="small" onClick={refreshOutbox} data-testid="refresh-outbox-btn">
                  读取队列
                </Button>
                <Button size="small" danger onClick={async () => { await clearAllWrites(); refreshOutbox(); }} data-testid="clear-outbox-btn">
                  清空队列
                </Button>
              </Space>
            </div>

            <div style={{ marginBottom: 16 }}>
              <Space>
                <Text type="secondary">模拟存储不可用 (故障注入):</Text>
                <Switch
                  size="small"
                  checked={storageFault}
                  onChange={handleToggleFault}
                  data-testid="toggle-storage-fault"
                />
              </Space>
            </div>

            {outboxError && (
              <div style={{ color: "#ff4d4f", fontSize: 13, marginBottom: 8 }} data-testid="outbox-error-msg">
                错误: {outboxError}
              </div>
            )}

            <div
              style={{
                backgroundColor: "#fafafa",
                padding: 10,
                borderRadius: 4,
                maxHeight: 180,
                overflowY: "auto",
                fontFamily: "monospace",
                fontSize: 12,
              }}
              data-testid="outbox-items-list"
            >
              {outboxItems.length === 0 ? (
                <Text type="secondary">队列为空</Text>
              ) : (
                outboxItems.map((it) => (
                  <div key={it.operation_id} style={{ marginBottom: 6, borderBottom: "1px dashed #eee", paddingBottom: 4 }}>
                    <div><strong>{it.operation_id}</strong> [{it.state}]</div>
                    <div style={{ color: "#8c8c8c" }}>{it.path}</div>
                  </div>
                ))
              )}
            </div>
          </Card>
        </Col>
      </Row>
    </div>
  );
};
