import React from "react";
import { Card, Button, Pagination, Typography } from "antd";
import { CheckOutlined, CloseOutlined } from "@ant-design/icons";

const { Text } = Typography;

export interface AnswerRecordItem {
  ordinal: number;
  instanceId?: string;
  status: "unanswered" | "correct" | "incorrect" | "current" | "pending";
}

export interface AnswerSheetProps {
  records: AnswerRecordItem[];
  currentOrdinal?: number;
  viewOrdinal?: number;
  pageSize?: number;
  totalCount?: number;
  onSelectOrdinal?: (ordinal: number) => void;
  onReturnToCurrent?: () => void;
  counts?: {
    answered: number;
    correct: number;
    incorrect: number;
  };
  mode?: "practice" | "exam";
}

export const AnswerSheet: React.FC<AnswerSheetProps> = ({
  records,
  currentOrdinal = 0,
  viewOrdinal = 0,
  pageSize = 50,
  onSelectOrdinal,
  onReturnToCurrent,
  counts,
  mode = "practice",
}) => {
  const [currentPage, setCurrentPage] = React.useState(1);

  React.useEffect(() => {
    if (viewOrdinal >= 0 && pageSize > 0) {
      const targetPage = Math.floor(viewOrdinal / pageSize) + 1;
      setCurrentPage((prev) => (prev !== targetPage ? targetPage : prev));
    }
  }, [viewOrdinal, pageSize]);

  const maxOrdinal = records.reduce((max, r) => Math.max(max, r.ordinal), currentOrdinal ?? 0);
  const totalItems = Math.max(records.length, maxOrdinal + 1);

  const startOrdinal = (currentPage - 1) * pageSize;
  const endOrdinal = startOrdinal + pageSize;
  const pageItems = records.filter((r) => r.ordinal >= startOrdinal && r.ordinal < endOrdinal);

  return (
    <Card
      size="small"
      title="答题卡"
      extra={
        onReturnToCurrent && viewOrdinal !== currentOrdinal && (
          <Button type="link" size="small" onClick={onReturnToCurrent} data-testid="return-current-btn">
            回到当前题
          </Button>
        )
      }
      style={{ width: 220, maxWidth: "100%" }}
      data-testid="answer-sheet"
    >
      {counts && mode === "practice" && (
        <div style={{ marginBottom: 12 }}>
          <Text type="secondary" style={{ fontSize: 12 }}>
            已答 {counts.answered} · 答对 {counts.correct} · 答错 {counts.incorrect}
          </Text>
        </div>
      )}
      <div
        style={{
          display: "grid",
          gridTemplateColumns: "repeat(5, 1fr)",
          gap: 6,
          marginBottom: 12,
        }}
      >
        {pageItems.map((rec) => {
          const isCurrent = rec.ordinal === viewOrdinal;
          let icon: React.ReactNode = null;
          let borderColor = "#d9d9d9";
          let bg = "#ffffff";
          let color = "#1f1f1f";

          if (mode === "practice") {
            if (rec.status === "correct") {
              bg = "#f6ffed";
              borderColor = "#52c41a";
              color = "#389e0d";
              icon = <CheckOutlined style={{ fontSize: 9 }} />;
            } else if (rec.status === "incorrect") {
              bg = "#fff2f0";
              borderColor = "#ff4d4f";
              color = "#cf1322";
              icon = <CloseOutlined style={{ fontSize: 9 }} />;
            }
          } else if (mode === "exam") {
            if (rec.status !== "unanswered") {
              bg = "#e6f4ff";
              borderColor = "#91caff";
            }
          }

          if (isCurrent) {
            borderColor = "#1677ff";
          }

          return (
            <Button
              key={rec.ordinal}
              size="small"
              data-testid={`sheet-item-${rec.ordinal}`}
              onClick={() => onSelectOrdinal?.(rec.ordinal)}
              style={{
                padding: 0,
                height: 32,
                minWidth: 32,
                fontSize: 12,
                borderColor,
                backgroundColor: bg,
                color,
                borderWidth: isCurrent ? 2 : 1,
                position: "relative",
                display: "flex",
                alignItems: "center",
                justifyContent: "center",
              }}
            >
              <span>{rec.ordinal + 1}</span>
              {icon && (
                <span
                  style={{
                    position: "absolute",
                    top: 1,
                    right: 2,
                    lineHeight: 1,
                  }}
                >
                  {icon}
                </span>
              )}
            </Button>
          );
        })}
      </div>
      {totalItems > pageSize && (
        <Pagination
          simple
          size="small"
          current={currentPage}
          pageSize={pageSize}
          total={totalItems}
          onChange={(p) => setCurrentPage(p)}
        />
      )}
    </Card>
  );
};
