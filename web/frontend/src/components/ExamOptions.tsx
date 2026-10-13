import React, { useEffect, useCallback } from "react";
import { Button, Space } from "antd";

export interface OptionItem {
  id: string;
  text: string;
}

export interface ExamOptionsProps {
  options: OptionItem[];
  selectedOptionId?: string;
  status?: "answering" | "saving" | "saved" | "reviewing";
  onSelect: (optionId: string) => void;
  onClear?: () => void;
  disabled?: boolean;
}

const indexLetters = ["A", "B", "C", "D"];

export const ExamOptions: React.FC<ExamOptionsProps> = ({
  options,
  selectedOptionId,
  status = "answering",
  onSelect,
  onClear,
  disabled = false,
}) => {
  const isReadOnly = disabled || status === "reviewing";

  const handleKeySelect = useCallback(
    (idx: number) => {
      if (isReadOnly || idx < 0 || idx >= options.length) return;
      onSelect(options[idx].id);
    },
    [isReadOnly, options, onSelect]
  );

  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
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
    <div data-testid="exam-options">
      <Space direction="vertical" style={{ width: "100%" }} size={12}>
        {options.map((option, idx) => {
          const letter = indexLetters[idx] || `${idx + 1}`;
          const isSelected = selectedOptionId === option.id;

          return (
            <Button
              key={option.id}
              data-testid={`exam-option-${idx}`}
              type={isSelected ? "primary" : "default"}
              disabled={isReadOnly}
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
                fontSize: 15,
                backgroundColor: isSelected ? "#e6f4ff" : "#ffffff",
                borderColor: isSelected ? "#1677ff" : "#d9d9d9",
                borderWidth: isSelected ? 2 : 1,
                color: isSelected ? "#0958d9" : "#1f1f1f",
              }}
            >
              <span
                style={{
                  display: "inline-block",
                  width: 24,
                  fontWeight: 600,
                  marginRight: 8,
                  color: isSelected ? "#1677ff" : "#8c8c8c",
                }}
              >
                {letter}
              </span>
              <span style={{ flex: 1 }}>{option.text}</span>
            </Button>
          );
        })}
      </Space>
      {onClear && !isReadOnly && selectedOptionId && (
        <div style={{ marginTop: 12 }}>
          <Button size="small" type="link" onClick={onClear} data-testid="clear-selection-btn">
            清除选择
          </Button>
        </div>
      )}
    </div>
  );
};
