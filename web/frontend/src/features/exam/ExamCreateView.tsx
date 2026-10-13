import React, { useState, useEffect } from "react";
import { Card, Form, Select, InputNumber, Button, Typography, Alert, Space, Switch } from "antd";
import { FormOutlined, PlayCircleOutlined } from "@ant-design/icons";
import { getAvailableBanks, createExam } from "./api";

const { Title, Paragraph, Text } = Typography;

interface ExamCreateViewProps {
  onNavigate?: (path: string) => void;
}

interface BankOption {
  id: string;
  name: string;
  member_count?: number;
  task_counts?: Record<string, number>;
}

export const ExamCreateView: React.FC<ExamCreateViewProps> = ({ onNavigate }) => {
  const [banks, setBanks] = useState<BankOption[]>([]);
  const [loadingBanks, setLoadingBanks] = useState(true);
  const [creating, setCreating] = useState(false);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  const [form] = Form.useForm();
  const selectedBankId = Form.useWatch("bank_id", form);
  const selectedBank = banks.find((b) => b.id === selectedBankId);
  const [isTimed, setIsTimed] = useState(true);

  useEffect(() => {
    getAvailableBanks()
      .then((res) => {
        setBanks(res.items || []);
        if (res.items && res.items.length > 0) {
          form.setFieldsValue({ bank_id: res.items[0].id });
        }
      })
      .catch((err) => {
        setErrorMsg("加载题库列表失败: " + (err.message || "网络错误"));
      })
      .finally(() => setLoadingBanks(false));
  }, [form]);

  const handleSubmit = async (values: any) => {
    setCreating(true);
    setErrorMsg(null);
    try {
      const timeLimitSeconds = isTimed && values.time_limit_minutes
        ? values.time_limit_minutes * 60
        : undefined;

      const res = await createExam({
        bank_id: values.bank_id,
        question_count: values.question_count || 20,
        time_limit_seconds: timeLimitSeconds,
      });

      if (res?.exam_id) {
        onNavigate?.(`/exams/${res.exam_id}`);
      } else {
        throw new Error("未能成功创建考试");
      }
    } catch (err: any) {
      const rawMsg = err.message || "";
      if (rawMsg.includes("Exam constraints could not be satisfied")) {
        setErrorMsg("所选题库的可用题量或题型分布不足以满足均衡组卷要求（默认在各类题型中均衡抽取）。建议减少题目数量，或选择收录更充裕的题库。");
      } else {
        setErrorMsg(rawMsg || "创建考试失败，可能是题库题目不足或组卷无解");
      }
    } finally {
      setCreating(false);
    }
  };

  return (
    <div style={{ maxWidth: 600, margin: "24px auto", padding: "0 16px" }} data-testid="exam-create-view">
      <Card>
        <Space direction="vertical" size={16} style={{ width: "100%" }}>
          <div>
            <Title level={3} style={{ marginBottom: 4 }}>
              <FormOutlined style={{ marginRight: 8, color: "#1677ff" }} />
              阶段考试
            </Title>
            <Paragraph type="secondary">
              在无答案提示与即时判分的环境下检验学习掌握情况。交卷后生成完整答题与改答分析报告。
            </Paragraph>
          </div>

          {errorMsg && (
            <Alert
              type="error"
              showIcon
              message={errorMsg}
              closable
              onClose={() => setErrorMsg(null)}
              data-testid="create-error-alert"
            />
          )}

          <Form
            form={form}
            layout="vertical"
            initialValues={{
              question_count: 20,
              time_limit_minutes: 20,
            }}
            onFinish={handleSubmit}
          >
            <Form.Item
              label="选择题库"
              name="bank_id"
              rules={[{ required: true, message: "请选择所属题库" }]}
              extra={
                selectedBank ? (
                  <span style={{ fontSize: 12, color: "#8c8c8c" }}>
                    题库容量：共 {selectedBank.member_count ?? 0} 词
                    {selectedBank.task_counts && (
                      <span>
                        （词义识别: {selectedBank.task_counts.meaning_recognition ?? 0}，反向选择: {selectedBank.task_counts.reverse_selection ?? 0}，语境选择: {selectedBank.task_counts.context_selection ?? 0}）
                      </span>
                    )}
                  </span>
                ) : undefined
              }
            >
              <Select
                placeholder="请选择题库"
                loading={loadingBanks}
                options={banks.map((b) => ({ label: b.name, value: b.id }))}
                data-testid="select-bank"
              />
            </Form.Item>

            <Form.Item
              label="题目数量 (1 - 200)"
              name="question_count"
              rules={[{ required: true, message: "请输入题目数量" }]}
              extra="默认建议 20 题，组卷算法将平衡各任务族题型"
            >
              <InputNumber min={1} max={200} style={{ width: "100%" }} data-testid="input-question-count" />
            </Form.Item>

            <Form.Item label="考试限时">
              <Space direction="vertical" style={{ width: "100%" }}>
                <Space>
                  <Switch checked={isTimed} onChange={setIsTimed} data-testid="switch-timed" />
                  <Text>{isTimed ? "启用倒计时限时" : "不限时"}</Text>
                </Space>
                {isTimed && (
                  <Form.Item
                    name="time_limit_minutes"
                    rules={[{ required: true, message: "请输入限时分钟数" }]}
                    style={{ marginBottom: 0, marginTop: 8 }}
                  >
                    <InputNumber
                      min={1}
                      max={240}
                      addonAfter="分钟"
                      style={{ width: "100%" }}
                      data-testid="input-time-limit"
                    />
                  </Form.Item>
                )}
              </Space>
            </Form.Item>

            <Form.Item style={{ marginTop: 24 }}>
              <Button
                type="primary"
                htmlType="submit"
                size="large"
                block
                icon={<PlayCircleOutlined />}
                loading={creating}
                data-testid="start-exam-btn"
              >
                开始考试
              </Button>
            </Form.Item>
          </Form>
        </Space>
      </Card>
    </div>
  );
};
