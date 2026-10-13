import React from "react";
import { Alert, Button, Typography } from "antd";
import { ArrowRightOutlined } from "@ant-design/icons";

const { Paragraph, Text } = Typography;

export interface FeedbackPanelProps {
  status: "correct" | "incorrect" | "reviewing" | "submitting" | "unknown";
  isCorrect?: boolean;
  userAnswerText?: string;
  correctAnswerText?: string;
  explanation?: string;
  onNext?: () => void;
  nextLoading?: boolean;
  nextDisabled?: boolean;
  autoAdvanceEnabled?: boolean;
}

export const FeedbackPanel: React.FC<FeedbackPanelProps> = ({
  status,
  isCorrect,
  userAnswerText,
  correctAnswerText,
  explanation,
  onNext,
  nextLoading = false,
  nextDisabled = false,
  autoAdvanceEnabled = false,
}) => {
  if (status === "submitting") {
    return (
      <Alert
        message="正在提交作答..."
        type="info"
        showIcon
        style={{ marginTop: 20 }}
        data-testid="submitting-alert"
      />
    );
  }

  if (status === "unknown") {
    return (
      <Alert
        message="作答状态待确认"
        description="网络响应暂未到达，系统正在安全恢复中，请勿重复作答。"
        type="warning"
        showIcon
        style={{ marginTop: 20 }}
        data-testid="unknown-alert"
      />
    );
  }

  const alertType = isCorrect ? "success" : "error";
  const title = isCorrect ? "回答正确" : "回答错误";

  return (
    <div style={{ marginTop: 20 }} data-testid="feedback-panel">
      <Alert
        message={
          <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
            <span style={{ fontWeight: 600 }}>{title}</span>
            {isCorrect && autoAdvanceEnabled && (
              <Text type="secondary" style={{ fontSize: 13 }}>
                0.5 秒后自动进入下一题
              </Text>
            )}
          </div>
        }
        description={
          <div>
            {!isCorrect && (
              <div style={{ margin: "8px 0" }}>
                {userAnswerText && <div>你的选择：<Text delete type="danger">{userAnswerText}</Text></div>}
                {correctAnswerText && <div>正确答案：<Text strong type="success">{correctAnswerText}</Text></div>}
              </div>
            )}
            {explanation && (
              <div style={{ marginTop: 8 }}>
                <Text type="secondary">解析：</Text>
                <Paragraph style={{ marginBottom: 0, marginTop: 4 }}>{explanation}</Paragraph>
              </div>
            )}
          </div>
        }
        type={alertType}
        showIcon
      />
      {onNext && (
        <div style={{ marginTop: 16, display: "flex", justifyContent: "flex-end" }}>
          <Button
            type="primary"
            onClick={onNext}
            loading={nextLoading}
            disabled={nextDisabled}
            icon={<ArrowRightOutlined />}
            data-testid="next-question-btn"
          >
            下一题 (D)
          </Button>
        </div>
      )}
    </div>
  );
};
