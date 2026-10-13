import React, { useEffect, useCallback } from "react";
import { Button, Space } from "antd";
import { CheckCircleOutlined, CloseCircleOutlined } from "@ant-design/icons";

export interface OptionItem {
  id: string;
  text: string;
}

export interface PracticeOptionsProps {
  options: OptionItem[];
  selectedOptionId?: string;
  correctOptionId?: string;
  status: "unanswered" | "submitting" | "submit-unknown" | "correct-feedback" | "incorrect-feedback" | "reviewing";
  onSelect: (optionId: string) => void;
  disabled?: boolean;
}

const indexKeys = ["1", "2", "3", "4"];
const indexLetters = ["A", "B", "C", "D"];

export const PracticeOptions: React.FC<PracticeOptionsProps> = ({
  options,
  selectedOptionId,
  correctOptionId,
  status,
  onSelect,
  disabled = false,
}) => {
  const isReadOnly = status !== "unanswered" || disabled;

  const handleKeySelect = useCallback(
    (idx: number) => {
      if (isReadOnly || idx < 0 || idx >= options.length) return;
      onSelect(options[idx].id);
    },
    [isReadOnly, options, onSelect]
  );

  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      // Ignore if user is typing in input or textarea
      const tag = (e.target as HTMLElement)?.tagName;
      if (["INPUT", "TEXTAREA", "SELECT"].includes(tag) || (e.target as HTMLElement)?.isContentEditable) {
        return;
      }
      if (e.repeat || e.ctrlKey || e.metaKey || e.altKey) {
        return;
      }
      if (["1", "2", "3", "4"].includes(e.key)) {
        const idx = parseInt(e.key, 10) - 1;
        handleKeySelect(idx);
      }
    };
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [handleKeySelect]);

  return (
    <Space direction="vertical" style={{ width: "100%" }} size={12} data-testid="practice-options">
      {options.map((option, idx) => {
        const letter = indexLetters[idx] || `${idx + 1}`;
        const keyNumber = indexKeys[idx] || "";
        const isSelected = selectedOptionId === option.id;
        const isCorrect = correctOptionId === option.id;

        let icon: React.ReactNode = null;
        let borderColor = "#d9d9d9";
        let backgroundColor = "#ffffff";
        let color = "#1f1f1f";

        if (status === "correct-feedback") {
          if (isCorrect || isSelected) {
            borderColor = "#52c41a";
            backgroundColor = "#f6ffed";
            color = "#389e0d";
            icon = <CheckCircleOutlined style={{ color: "#52c41a" }} />;
          }
        } else if (status === "incorrect-feedback") {
          if (isSelected) {
            borderColor = "#ff4d4f";
            backgroundColor = "#fff2f0";
            color = "#cf1322";
            icon = <CloseCircleOutlined style={{ color: "#ff4d4f" }} />;
          } else if (isCorrect) {
            borderColor = "#52c41a";
            backgroundColor = "#f6ffed";
            color = "#389e0d";
            icon = <CheckCircleOutlined style={{ color: "#52c41a" }} />;
          }
        } else if (status === "reviewing") {
          if (isCorrect) {
            borderColor = "#52c41a";
            backgroundColor = "#f6ffed";
            icon = <CheckCircleOutlined style={{ color: "#52c41a" }} />;
          } else if (isSelected) {
            borderColor = "#ff4d4f";
            backgroundColor = "#fff2f0";
            icon = <CloseCircleOutlined style={{ color: "#ff4d4f" }} />;
          }
        } else if (status === "submitting" && isSelected) {
          borderColor = "#1677ff";
          backgroundColor = "#e6f4ff";
          color = "#0958d9";
        }

        return (
          <Button
            key={option.id}
            data-testid={`option-item-${idx}`}
            data-option-id={option.id}
            disabled={isReadOnly}
            loading={status === "submitting" && isSelected}
            onClick={() => onSelect(option.id)}
            style={{
              width: "100%",
              height: "auto",
              minHeight: 48,
              padding: "10px 16px",
              display: "flex",
              alignItems: "center",
              justifyContent: "flex-start",
              textAlign: "left",
              whiteSpace: "normal",
              borderColor,
              backgroundColor,
              color,
              fontSize: 15,
            }}
          >
            <span
              style={{
                display: "inline-block",
                width: 24,
                fontWeight: 600,
                color: "#8c8c8c",
                marginRight: 8,
              }}
            >
              {letter}
            </span>
            <span style={{ flex: 1 }}>{option.text}</span>
            {icon && <span style={{ marginLeft: 8 }}>{icon}</span>}
            {!isReadOnly && keyNumber && (
              <span style={{ marginLeft: 8, fontSize: 12, color: "#bfbfbf" }}>({keyNumber})</span>
            )}
          </Button>
        );
      })}
    </Space>
  );
};
