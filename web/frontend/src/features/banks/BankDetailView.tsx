import React, { useState, useEffect, useRef } from "react";
import {
  Card,
  Button,
  Tag,
  Modal,
  Table,
  Tabs,
  Space,
  Typography,
  Alert,
  message,
  Statistic,
  Row,
  Col,
} from "antd";
import {
  ArrowLeftOutlined,
  PlayCircleOutlined,
  FormOutlined,
  InboxOutlined,
  DeleteOutlined,
  ReloadOutlined,
  UploadOutlined,
} from "@ant-design/icons";
import { getSecureRandomUUID } from "../../utils/uuid";
import {
  getBankDetail,
  archiveBank,
  removeBankMembers,
  listSenseVersions,
  listQuestionVersions,
  getActiveSession,
  ApiError,
} from "./api";
import {
  BankDetail,
  SenseVersionItem,
  QuestionVersionItem,
} from "./types";

const { Title, Text, Paragraph } = Typography;

interface BankDetailViewProps {
  bankId: string;
  onNavigate?: (path: string) => void;
}

export const BankDetailView: React.FC<BankDetailViewProps> = ({ bankId, onNavigate }) => {
  const [bank, setBank] = useState<BankDetail | null>(null);
  const [loading, setLoading] = useState<boolean>(true);
  const [sessionLocked, setSessionLocked] = useState<boolean>(false);
  const [activeSession, setActiveSession] = useState<any | null>(null);

  // Members selection
  const [selectedSenseIds, setSelectedSenseIds] = useState<React.Key[]>([]);
  const [removingMembers, setRemovingMembers] = useState<boolean>(false);

  // Archive modal
  const [isArchiveModalOpen, setIsArchiveModalOpen] = useState<boolean>(false);
  const [archiving, setArchiving] = useState<boolean>(false);

  // Content version discovery
  const [senseVersions, setSenseVersions] = useState<SenseVersionItem[]>([]);
  const [senseCursor, setSenseCursor] = useState<string | null>(null);
  const [senseHasMore, setSenseHasMore] = useState<boolean>(false);
  const [loadingSenses, setLoadingSenses] = useState<boolean>(false);

  const [questionVersions, setQuestionVersions] = useState<QuestionVersionItem[]>([]);
  const [questionCursor, setQuestionCursor] = useState<string | null>(null);
  const [questionHasMore, setQuestionHasMore] = useState<boolean>(false);
  const [loadingQuestions, setLoadingQuestions] = useState<boolean>(false);
  const bankRequestRef = useRef<number>(0);
  const sensesRequestRef = useRef<number>(0);
  const questionsRequestRef = useRef<number>(0);

  const loadBankDetail = async () => {
    const requestId = ++bankRequestRef.current;
    setLoading(true);
    setSessionLocked(false);
    try {
      const res = await getBankDetail(bankId);
      if (bankRequestRef.current !== requestId) return;
      setBank(res.bank);
      setSelectedSenseIds([]);
    } catch (err: any) {
      if (bankRequestRef.current !== requestId) return;
      if (err instanceof ApiError && err.code === "SESSION_ACTIVE") {
        setSessionLocked(true);
        getActiveSession()
          .then((s) => setActiveSession(s.session))
          .catch(() => {});
      } else {
        message.error(err.message || "加载题库详情失败");
      }
    } finally {
      if (bankRequestRef.current === requestId) setLoading(false);
    }
  };

  const loadSenses = async (cursor?: string, append = false) => {
    const requestId = ++sensesRequestRef.current;
    setLoadingSenses(true);
    try {
      const res = await listSenseVersions({
        bank_id: bankId,
        cursor,
        limit: 10,
      });
      if (sensesRequestRef.current !== requestId) return;
      setSenseVersions((prev) => (append ? [...prev, ...res.items] : res.items));
      setSenseCursor(res.next_cursor);
      setSenseHasMore(res.has_more);
    } catch (err: any) {
      if (sensesRequestRef.current !== requestId) return;
      if (err instanceof ApiError && err.code === "SESSION_ACTIVE") {
        setSessionLocked(true);
      } else {
        message.error(err.message || "加载词义版本失败");
      }
    } finally {
      if (sensesRequestRef.current === requestId) setLoadingSenses(false);
    }
  };

  const loadQuestions = async (cursor?: string, append = false) => {
    const requestId = ++questionsRequestRef.current;
    setLoadingQuestions(true);
    try {
      const res = await listQuestionVersions({
        bank_id: bankId,
        cursor,
        limit: 10,
      });
      if (questionsRequestRef.current !== requestId) return;
      setQuestionVersions((prev) => (append ? [...prev, ...res.items] : res.items));
      setQuestionCursor(res.next_cursor);
      setQuestionHasMore(res.has_more);
    } catch (err: any) {
      if (questionsRequestRef.current !== requestId) return;
      if (err instanceof ApiError && err.code === "SESSION_ACTIVE") {
        setSessionLocked(true);
      } else {
        message.error(err.message || "加载题目版本失败");
      }
    } finally {
      if (questionsRequestRef.current === requestId) setLoadingQuestions(false);
    }
  };

  useEffect(() => {
    loadBankDetail();
    loadSenses();
    loadQuestions();
    return () => {
      bankRequestRef.current++;
      sensesRequestRef.current++;
      questionsRequestRef.current++;
    };
  }, [bankId]);

  const handleArchive = async () => {
    if (!bank) return;
    setArchiving(true);
    try {
      const idempotencyKey = getSecureRandomUUID();
      await archiveBank(bank.id, bank.revision, idempotencyKey);
      message.success("题库已成功归档");
      setIsArchiveModalOpen(false);
      await loadBankDetail();
    } catch (err: any) {
      if (err instanceof ApiError) {
        if (err.code === "REVISION_CONFLICT") {
          message.error(`版本冲突 (当前版本 rev ${err.details?.current_revision})，已为您刷新`);
          await loadBankDetail();
        } else if (err.code === "MAINTENANCE") {
          message.error("系统处于维护模式，禁止归档题库");
        } else if (err.code === "SESSION_ACTIVE") {
          message.error("存在活动会话，禁止归档题库");
          setSessionLocked(true);
        } else {
          message.error(err.message);
        }
      } else {
        message.error("归档失败");
      }
    } finally {
      setArchiving(false);
    }
  };

  const handleRemoveMembers = async () => {
    if (!bank || selectedSenseIds.length === 0) return;
    setRemovingMembers(true);
    try {
      const idempotencyKey = getSecureRandomUUID();
      const res = await removeBankMembers(
        bank.id,
        selectedSenseIds as string[],
        bank.revision,
        idempotencyKey
      );
      message.success(`成功移除 ${res.removed_count} 个成员`);
      setSelectedSenseIds([]);
      await loadBankDetail();
    } catch (err: any) {
      if (err instanceof ApiError) {
        if (err.code === "REVISION_CONFLICT") {
          message.error(`版本冲突 (当前版本 rev ${err.details?.current_revision})，已为您刷新`);
          await loadBankDetail();
        } else if (err.code === "MAINTENANCE") {
          message.error("系统处于维护模式，禁止修改题库成员");
        } else if (err.code === "SESSION_ACTIVE") {
          message.error("存在活动会话，题库修改已被锁定");
          setSessionLocked(true);
        } else {
          message.error(err.message);
        }
      } else {
        message.error("移除成员失败");
      }
    } finally {
      setRemovingMembers(false);
    }
  };

  const memberColumns = [
    {
      title: "规范词形",
      dataIndex: "canonical_form",
      key: "canonical_form",
      render: (text: string) => <Text strong>{text}</Text>,
    },
    {
      title: "词性",
      dataIndex: "part_of_speech",
      key: "part_of_speech",
      render: (pos: string) => <Tag color="blue">{pos}</Tag>,
    },
    {
      title: "释义",
      dataIndex: "meaning",
      key: "meaning",
    },
    {
      title: "Sense ID",
      dataIndex: "sense_id",
      key: "sense_id",
      ellipsis: true,
      render: (id: string) => <Text code>{id.slice(0, 8)}...</Text>,
    },
    {
      title: "来源 ID",
      dataIndex: "source_id",
      key: "source_id",
      ellipsis: true,
      render: (id: string) => <Text code>{id.slice(0, 8)}...</Text>,
    },
  ];

  const senseColumns = [
    {
      title: "词义版本 ID",
      dataIndex: "sense_version_id",
      key: "sense_version_id",
      ellipsis: true,
      render: (id: string) => <Text code title={id}>{id.length > 12 ? `${id.slice(0, 12)}…` : id}</Text>,
    },
    {
      title: "规范词形",
      dataIndex: "canonical_form",
      key: "canonical_form",
      render: (text: string) => <Text strong>{text}</Text>,
    },
    {
      title: "释义",
      dataIndex: "definition",
      key: "definition",
    },
    {
      title: "词位 ID",
      dataIndex: "lexeme_id",
      key: "lexeme_id",
      ellipsis: true,
      render: (id: string) => <Text code>{id.slice(0, 8)}...</Text>,
    },
    {
      title: "状态",
      dataIndex: "status",
      key: "status",
      render: (s: string) => <Tag color="green">{s}</Tag>,
    },
  ];

  const questionColumns = [
    {
      title: "题目版本 ID",
      dataIndex: "question_version_id",
      key: "question_version_id",
      ellipsis: true,
      render: (id: string) => <Text code title={id}>{id.length > 12 ? `${id.slice(0, 12)}…` : id}</Text>,
    },
    {
      title: "题型",
      dataIndex: "task_family",
      key: "task_family",
      render: (tf: string) => <Tag color="purple">{tf}</Tag>,
    },
    {
      title: "题型模板 (Form ID)",
      dataIndex: "form_id",
      key: "form_id",
      render: (id: string) => <Text code>{id.slice(0, 8)}...</Text>,
    },
    {
      title: "状态",
      dataIndex: "status",
      key: "status",
      render: (s: string) => <Tag color="cyan">{s}</Tag>,
    },
  ];

  if (sessionLocked) {
    return (
      <div style={{ maxWidth: 1000, margin: "0 auto", padding: "24px 0" }} data-testid="bank-detail-locked-view">
        <Button
          icon={<ArrowLeftOutlined />}
          onClick={() => onNavigate?.("/banks")}
          style={{ marginBottom: 16 }}
          data-testid="back-to-banks-btn"
        >
          返回题库列表
        </Button>
        <Alert
          message="题库内容与版本已锁定 (409 Conflict)"
          description="系统检测到当前有正在进行的学习会话（自适应练习或阶段考试）。为保证考核公正与防泄题机制，题库详细内容及题目发现接口已临时锁定保护。请先完成或结束当前会话后再次查看。"
          type="warning"
          showIcon
          action={
            <Button
              type="primary"
              onClick={() => {
                if (activeSession?.kind === "practice") {
                  onNavigate?.("/practice");
                } else if (activeSession?.id) {
                  onNavigate?.(`/exams/${activeSession.id}`);
                } else {
                  onNavigate?.("/practice");
                }
              }}
              data-testid="continue-session-lock-btn"
            >
              继续活动会话
            </Button>
          }
          data-testid="session-active-lock-alert"
        />
      </div>
    );
  }

  return (
    <div style={{ maxWidth: 1100, margin: "0 auto", padding: "24px 0" }} data-testid="bank-detail-view">
      <div style={{ marginBottom: 16 }}>
        <Button
          icon={<ArrowLeftOutlined />}
          onClick={() => onNavigate?.("/banks")}
          data-testid="back-to-banks-btn"
        >
          返回题库列表
        </Button>
      </div>

      {bank && (
        <>
          <Card loading={loading} style={{ marginBottom: 24 }}>
            <div
              style={{
                display: "flex",
                justifyContent: "space-between",
                alignItems: "flex-start",
                flexWrap: "wrap",
                gap: 16,
                marginBottom: 16,
              }}
            >
              <div style={{ minWidth: 200, flex: "1 1 auto" }}>
                <Space align="center" wrap>
                  <Title level={3} style={{ margin: 0 }} data-testid="bank-detail-title">
                    {bank.name}
                  </Title>
                  {bank.archived ? (
                    <Tag color="default" data-testid="bank-status-tag">
                      已归档
                    </Tag>
                  ) : (
                    <Tag color="success" data-testid="bank-status-tag">
                      正常
                    </Tag>
                  )}
                  <Tag color="blue" data-testid="bank-revision-tag">
                    rev {bank.revision}
                  </Tag>
                </Space>
                <Paragraph type="secondary" style={{ marginTop: 8, marginBottom: 0 }}>
                  {bank.note || "暂无备注"}
                </Paragraph>
              </div>

              <Space wrap size={[8, 8]} style={{ maxWidth: "100%" }}>
                <Button
                  type="primary"
                  icon={<PlayCircleOutlined />}
                  disabled={bank.archived}
                  onClick={() => onNavigate?.(`/practice?bank_id=${bank.id}`)}
                  data-testid="detail-practice-btn"
                >
                  开始练习
                </Button>
                <Button
                  icon={<FormOutlined />}
                  disabled={bank.archived}
                  onClick={() => onNavigate?.(`/exams/new?bank_id=${bank.id}`)}
                  data-testid="detail-exam-btn"
                >
                  新建考试
                </Button>
                <Button
                  icon={<UploadOutlined />}
                  disabled={bank.archived}
                  onClick={() => onNavigate?.(`/imports?bank_id=${bank.id}`)}
                  data-testid="detail-import-btn"
                >
                  导入词表
                </Button>
                {!bank.archived && (
                  <Button
                    danger
                    icon={<InboxOutlined />}
                    onClick={() => setIsArchiveModalOpen(true)}
                    data-testid="archive-bank-btn"
                  >
                    归档题库
                  </Button>
                )}
              </Space>
            </div>

            <Row gutter={[16, 16]}>
              <Col xs={12} sm={12} md={6}>
                <Statistic title="词义成员总数" value={bank.member_count} />
              </Col>
              <Col xs={12} sm={12} md={6}>
                <Statistic
                  title="词义辨析 (meaning_rec)"
                  value={bank.task_counts?.meaning_recognition || 0}
                />
              </Col>
              <Col xs={12} sm={12} md={6}>
                <Statistic
                  title="反向选择 (reverse_sel)"
                  value={bank.task_counts?.reverse_selection || 0}
                />
              </Col>
              <Col xs={12} sm={12} md={6}>
                <Statistic
                  title="语境选择 (context_sel)"
                  value={bank.task_counts?.context_selection || 0}
                />
              </Col>
            </Row>
          </Card>

          <Card>
            <Tabs
              defaultActiveKey="members"
              items={[
                {
                  key: "members",
                  label: "词义成员",
                  children: (
                    <div data-testid="tab-members">
                      <div style={{ marginBottom: 16, display: "flex", justifyContent: "space-between", alignItems: "center", flexWrap: "wrap", gap: 8 }}>
                        <Text type="secondary" style={{ flex: "1 1 240px", minWidth: 0 }}>题库包含的词义定义与成员列表</Text>
                        <Button
                          danger
                          icon={<DeleteOutlined />}
                          disabled={selectedSenseIds.length === 0 || bank.archived}
                          loading={removingMembers}
                          onClick={handleRemoveMembers}
                          data-testid="remove-members-btn"
                        >
                          移除所选成员 ({selectedSenseIds.length})
                        </Button>
                      </div>
                      <Table
                        rowKey="sense_id"
                        rowSelection={{
                          selectedRowKeys: selectedSenseIds,
                          onChange: setSelectedSenseIds,
                        }}
                        columns={memberColumns}
                        dataSource={bank.members}
                        pagination={{ pageSize: 15 }}
                        data-testid="members-table"
                      />
                    </div>
                  ),
                },
                {
                  key: "senses",
                  label: "词义版本发现",
                  children: (
                    <div data-testid="tab-senses">
                      <div style={{ marginBottom: 16, display: "flex", justifyContent: "space-between", alignItems: "center", flexWrap: "wrap", gap: 8 }}>
                        <Text type="secondary" style={{ flex: "1 1 240px", minWidth: 0 }}>真实词义版本发现（稳定 Opaque Cursor 游标分页）</Text>
                        <Button
                          icon={<ReloadOutlined />}
                          size="small"
                          onClick={() => loadSenses()}
                          loading={loadingSenses}
                        >
                          刷新
                        </Button>
                      </div>
                      <Table
                        rowKey="sense_version_id"
                        columns={senseColumns}
                        dataSource={senseVersions}
                        pagination={false}
                        loading={loadingSenses}
                        scroll={{ x: 760 }}
                        data-testid="sense-versions-table"
                      />
                      {senseHasMore && (
                        <div style={{ textAlign: "center", marginTop: 16 }}>
                          <Button
                            onClick={() => loadSenses(senseCursor || undefined, true)}
                            loading={loadingSenses}
                            data-testid="load-more-senses-btn"
                          >
                            加载更多词义版本
                          </Button>
                        </div>
                      )}
                    </div>
                  ),
                },
                {
                  key: "questions",
                  label: "题目版本发现",
                  children: (
                    <div data-testid="tab-questions">
                      <div style={{ marginBottom: 16, display: "flex", justifyContent: "space-between", alignItems: "center", flexWrap: "wrap", gap: 8 }}>
                        <Text type="secondary" style={{ flex: "1 1 240px", minWidth: 0 }}>真实题目版本发现（默认不含答案，防泄题）</Text>
                        <Button
                          icon={<ReloadOutlined />}
                          size="small"
                          onClick={() => loadQuestions()}
                          loading={loadingQuestions}
                        >
                          刷新
                        </Button>
                      </div>
                      <Table
                        rowKey="question_version_id"
                        columns={questionColumns}
                        dataSource={questionVersions}
                        pagination={false}
                        loading={loadingQuestions}
                        scroll={{ x: 680 }}
                        data-testid="question-versions-table"
                      />
                      {questionHasMore && (
                        <div style={{ textAlign: "center", marginTop: 16 }}>
                          <Button
                            onClick={() => loadQuestions(questionCursor || undefined, true)}
                            loading={loadingQuestions}
                            data-testid="load-more-questions-btn"
                          >
                            加载更多题目版本
                          </Button>
                        </div>
                      )}
                    </div>
                  ),
                },
              ]}
            />
          </Card>
        </>
      )}

      {/* Archive Modal */}
      <Modal
        title="归档题库确认"
        open={isArchiveModalOpen}
        onCancel={() => setIsArchiveModalOpen(false)}
        onOk={handleArchive}
        confirmLoading={archiving}
        okText="确认归档"
        cancelText="取消"
        okButtonProps={{ danger: true, "data-testid": "confirm-archive-btn" } as any}
        data-testid="archive-confirm-modal"
      >
        <Paragraph>
          您确定要归档题库 <Text strong>{bank?.name}</Text> 吗？
        </Paragraph>
        <Paragraph type="secondary">
          归档后该题库将保留历史查阅入口，但禁止作为新的自适应练习或阶段考试载体。
        </Paragraph>
      </Modal>
    </div>
  );
};
