import React, { useState, useEffect } from "react";
import {
  Card,
  Row,
  Col,
  Statistic,
  Typography,
  Tag,
  Alert,
  Spin,
  Button,
  Space,
  Divider,
} from "antd";
import {
  CheckCircleOutlined,
  CloseCircleOutlined,
  MinusCircleOutlined,
  StopOutlined,
  RedoOutlined,
  HomeOutlined,
  TrophyOutlined,
} from "@ant-design/icons";
import { getExamResult } from "./api";
import { ExamResultResponse, ExamResultQuestionItem } from "./types";
import { QuestionStem, taskFamilyLabels } from "../../components/QuestionStem";

const { Title, Text, Paragraph } = Typography;

interface ExamResultViewProps {
  examId: string;
  onNavigate?: (path: string) => void;
}

export const ExamResultView: React.FC<ExamResultViewProps> = ({ examId, onNavigate }) => {
  const [result, setResult] = useState<ExamResultResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let mounted = true;
    setLoading(true);
    setError(null);

    getExamResult(examId)
      .then((data) => {
        if (!mounted) return;
        setResult(data);
      })
      .catch((err) => {
        if (!mounted) return;
        setError(err.message || "获取考试结果失败");
      })
      .finally(() => {
        if (mounted) setLoading(false);
      });

    return () => {
      mounted = false;
    };
  }, [examId]);

  if (loading) {
    return (
      <div style={{ textAlign: "center", padding: "64px 0" }} data-testid="result-loading">
        <Spin size="large" tip="正在计算考试成绩与答题分析..." />
      </div>
    );
  }

  if (error || !result) {
    return (
      <div style={{ maxWidth: 600, margin: "32px auto", padding: "0 16px" }}>
        <Alert
          type="error"
          showIcon
          message="加载结果失败"
          description={error || "尚未生成评分结果或考试不存在"}
          action={
            <Button size="small" onClick={() => onNavigate?.("/exams/new")}>
              返回创建
            </Button>
          }
          data-testid="result-error-alert"
        />
      </div>
    );
  }

  const {
    total,
    correct,
    omitted,
    void: voidCount,
    ratio,
    questions,
    response_analysis,
  } = result;

  const validDenominator = total - voidCount;
  const hasTelemetryIncomplete = questions.some((q) => q.response_history === "telemetry_incomplete");

  return (
    <div
      style={{
        maxWidth: 900,
        margin: "24px auto",
        padding: "0 16px",
      }}
      data-testid="exam-result-view"
    >
      {/* Top Header Card */}
      <Card style={{ marginBottom: 20 }}>
        <div style={{ display: "flex", justifyContent: "space-between", alignItems: "flex-start", flexWrap: "wrap", gap: 16 }}>
          <div>
            <Title level={3} style={{ margin: 0, display: "flex", alignItems: "center", gap: 8 }}>
              <TrophyOutlined style={{ color: "#faad14" }} />
              考试结果报告
            </Title>
            <Text type="secondary">试卷编号: {examId}</Text>
          </div>

          <Space>
            <Button icon={<RedoOutlined />} onClick={() => onNavigate?.("/exams/new")} data-testid="retry-exam-btn">
              重新开始
            </Button>
            <Button icon={<HomeOutlined />} onClick={() => onNavigate?.("/")} data-testid="back-home-btn">
              返回首页
            </Button>
          </Space>
        </div>

        <Divider style={{ margin: "16px 0" }} />

        {/* Primary Scores */}
        <Row gutter={[16, 16]}>
          <Col xs={12} sm={6}>
            <Statistic
              title="正确率"
              value={ratio !== null ? `${(ratio * 100).toFixed(1)}%` : "暂无有效题目"}
              valueStyle={{ color: ratio !== null && ratio >= 0.6 ? "#3f8600" : "#cf1322" }}
              data-testid="ratio-stat"
            />
          </Col>
          <Col xs={12} sm={6}>
            <Statistic
              title="答对 / 有效题数"
              value={`${correct} / ${validDenominator}`}
              data-testid="score-stat"
            />
          </Col>
          <Col xs={12} sm={6}>
            <Statistic title="未作答" value={omitted} data-testid="omitted-stat" />
          </Col>
          <Col xs={12} sm={6}>
            <Statistic title="已作废题目" value={voidCount} data-testid="void-stat" />
          </Col>
        </Row>

        {/* Response Analysis Section */}
        <Divider style={{ margin: "16px 0" }} />
        <div data-testid="response-analysis-panel">
          <Text strong style={{ fontSize: 13, marginBottom: 8, display: "block" }}>
            作答行为与改答分析
          </Text>
          {!hasTelemetryIncomplete && response_analysis && response_analysis.reliable_first_choices > 0 ? (
            <Space size={16} wrap>
              <Tag color="green">首次即选对: {response_analysis.first_choice_correct_count}</Tag>
              <Tag color="cyan">改答转为正确: {response_analysis.revised_to_correct_count}</Tag>
              <Tag color="volcano">改答转为错误: {response_analysis.revised_to_incorrect_count}</Tag>
              <Text type="secondary" style={{ fontSize: 12 }}>
                有效首次选择记录: {response_analysis.reliable_first_choices} 题
              </Text>
            </Space>
          ) : (
            <Alert
              type="info"
              showIcon
              message="无可靠首次选择记录"
              description="由于网络恢复或作答事件序列重建，部分遥测数据不完整，不作为错误判分依据。"
              data-testid="no-reliable-first-choice-alert"
            />
          )}
        </div>
      </Card>

      {/* Questions Breakdown */}
      <Title level={4} style={{ marginBottom: 16 }}>
        逐题详细解析 ({questions.length} 题)
      </Title>

      <Space direction="vertical" size={16} style={{ width: "100%" }}>
        {questions.map((q: ExamResultQuestionItem) => {
          let statusTag: React.ReactNode = null;
          let cardBorderColor = "#d9d9d9";

          if (q.task_family === "void" || q.explanation?.includes("作废") || (q.is_correct === false && q.selected_option_id === null && q.correct_option_id === "")) {
            statusTag = <Tag icon={<StopOutlined />} color="default">题目作废</Tag>;
          } else if (!q.selected_option_id) {
            statusTag = <Tag icon={<MinusCircleOutlined />} color="warning">未作答</Tag>;
          } else if (q.is_correct) {
            statusTag = <Tag icon={<CheckCircleOutlined />} color="success">回答正确</Tag>;
            cardBorderColor = "#b7eb8f";
          } else {
            statusTag = <Tag icon={<CloseCircleOutlined />} color="error">回答错误</Tag>;
            cardBorderColor = "#ffa39e";
          }

          return (
            <Card
              key={q.instance_id}
              size="small"
              style={{ borderColor: cardBorderColor }}
              data-testid={`result-question-${q.ordinal}`}
            >
              <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 12 }}>
                <Space>
                  <Text strong>第 {q.ordinal + 1} 题</Text>
                  <Tag color="blue">{taskFamilyLabels[q.task_family] || q.task_family}</Tag>
                </Space>
                <Space>
                  {q.first_correct === null && (
                    <Text type="secondary" style={{ fontSize: 12 }}>
                      (首次状态缺失)
                    </Text>
                  )}
                  {statusTag}
                </Space>
              </div>

              <QuestionStem stem={q.stem} />

              {/* Options list */}
              <div style={{ marginTop: 16, marginBottom: 16 }}>
                <Space direction="vertical" size={8} style={{ width: "100%" }}>
                  {q.options.map((opt, idx) => {
                    const letter = ["A", "B", "C", "D"][idx] || `${idx + 1}`;
                    const isSelected = q.selected_option_id === opt.id;
                    const isCorrect = q.correct_option_id === opt.id;

                    let bg = "#fafafa";
                    let border = "1px solid #d9d9d9";
                    let color = "#262626";

                    if (isCorrect) {
                      bg = "#f6ffed";
                      border = "1px solid #52c41a";
                      color = "#389e0d";
                    } else if (isSelected && !isCorrect) {
                      bg = "#fff2f0";
                      border = "1px solid #ff4d4f";
                      color = "#cf1322";
                    }

                    return (
                      <div
                        key={opt.id}
                        data-testid={`result-option-${q.ordinal}-${idx}`}
                        style={{
                          padding: "8px 12px",
                          borderRadius: 4,
                          backgroundColor: bg,
                          border,
                          color,
                          display: "flex",
                          alignItems: "center",
                          justifyContent: "space-between",
                        }}
                      >
                        <div>
                          <strong style={{ marginRight: 8 }}>{letter}.</strong>
                          <span>{opt.text}</span>
                        </div>
                        <Space>
                          {isSelected && <Tag color={isCorrect ? "success" : "error"}>你的选择</Tag>}
                          {isCorrect && <Tag color="success">正确答案</Tag>}
                        </Space>
                      </div>
                    );
                  })}
                </Space>
              </div>

              {/* Explanation & History */}
              <div style={{ backgroundColor: "#f5f5f5", padding: "10px 14px", borderRadius: 4 }}>
                <Text strong style={{ fontSize: 13, display: "block", marginBottom: 4 }}>
                  题目解析:
                </Text>
                <Paragraph style={{ margin: 0, fontSize: 13, color: "#595959" }} data-testid={`explanation-${q.ordinal}`}>
                  {q.explanation || "暂无详细解析"}
                </Paragraph>

                {q.response_history && (
                  <div style={{ marginTop: 8, fontSize: 12, color: "#8c8c8c" }}>
                    作答记录: {q.response_history}
                  </div>
                )}
              </div>
            </Card>
          );
        })}
      </Space>
    </div>
  );
};
