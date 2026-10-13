import React from "react";
import { Typography, Tag, Space } from "antd";

const { Title } = Typography;

export interface QuestionStemProps {
  stem: string;
  taskFamily?: string;
  ordinal?: number;
  totalCount?: number;
}

export const taskFamilyLabels: Record<string, string> = {
  meaning_recognition: "词义识别",
  reverse_selection: "反向选择",
  context_selection: "语境选择",
};

export const QuestionStem: React.FC<QuestionStemProps> = ({
  stem,
  taskFamily,
  ordinal,
  totalCount,
}) => {
  const familyLabel = taskFamily ? (taskFamilyLabels[taskFamily] || taskFamily) : undefined;
  const ordinalLabel = ordinal !== undefined ? `第 ${ordinal + 1} 题` : undefined;
  const hasMeta = Boolean(familyLabel || ordinalLabel);

  return (
    <div style={{ marginBottom: 24 }} data-testid="question-stem-container">
      {hasMeta && (
        <Space direction="horizontal" style={{ marginBottom: 12 }}>
          {familyLabel && <Tag color="blue">{familyLabel}</Tag>}
          {ordinalLabel && (
            <Typography.Text type="secondary">
              {ordinalLabel}
              {totalCount ? ` / 共 ${totalCount} 题` : ""}
            </Typography.Text>
          )}
        </Space>
      )}
      <Title level={4} style={{ marginTop: 0, marginBottom: 12, fontWeight: 500 }} data-testid="question-stem-text">
        {stem}
      </Title>
    </div>
  );
};
