import React, { useState, useEffect } from "react";
import {
  Card,
  Button,
  Input,
  Tag,
  Modal,
  Drawer,
  Form,
  Space,
  Typography,
  Empty,
  Alert,
  message,
  Row,
  Col,
  Statistic,
  Spin,
} from "antd";
import {
  PlusOutlined,
  FolderOpenOutlined,
  PlayCircleOutlined,
  FormOutlined,
  SearchOutlined,
} from "@ant-design/icons";
import { getSecureRandomUUID } from "../../utils/uuid";
import {
  listBanks,
  createBank,
  listSources,
  createSource,
  getActiveSession,
  ApiError,
} from "./api";
import { BankSummary, Source } from "./types";

const { Title, Text, Paragraph } = Typography;

interface BankListViewProps {
  onNavigate?: (path: string) => void;
}

export const BankListView: React.FC<BankListViewProps> = ({ onNavigate }) => {
  const [banks, setBanks] = useState<BankSummary[]>([]);
  const [loading, setLoading] = useState<boolean>(true);
  const [searchText, setSearchText] = useState<string>("");
  const [activeSession, setActiveSession] = useState<any | null>(null);

  // Create Bank Modal
  const [isCreateModalOpen, setIsCreateModalOpen] = useState<boolean>(false);
  const [createSubmitting, setCreateSubmitting] = useState<boolean>(false);
  const [bankForm] = Form.useForm();

  // Sources Drawer
  const [isSourceDrawerOpen, setIsSourceDrawerOpen] = useState<boolean>(false);
  const [sources, setSources] = useState<Source[]>([]);
  const [loadingSources, setLoadingSources] = useState<boolean>(false);
  const [sourcesError, setSourcesError] = useState<string | null>(null);
  const [sourceSubmitting, setSourceSubmitting] = useState<boolean>(false);
  const [sourceForm] = Form.useForm();

  const [banksError, setBanksError] = useState<string | null>(null);

  const loadData = async () => {
    setLoading(true);
    setBanksError(null);
    try {
      const [banksRes, sessionRes] = await Promise.all([
        listBanks(),
        getActiveSession().catch(() => ({ session: null })),
      ]);
      setBanks(banksRes.items || []);
      setActiveSession(sessionRes.session || null);
    } catch (err: any) {
      console.error(err);
      setBanksError(err?.message || "题库加载失败，请检查网络或稍后重试");
    } finally {
      setLoading(false);
    }
  };

  const loadSources = async () => {
    setLoadingSources(true);
    setSourcesError(null);
    try {
      const res = await listSources();
      setSources(res.items || []);
    } catch (err: any) {
      console.error(err);
      setSourcesError(err?.message || "来源列表加载失败，请稍后重试");
    } finally {
      setLoadingSources(false);
    }
  };

  useEffect(() => {
    loadData();
  }, []);

  const handleCreateBank = async (values: { name: string; note?: string }) => {
    setCreateSubmitting(true);
    try {
      const idempotencyKey = getSecureRandomUUID();
      await createBank({ name: values.name.trim(), note: values.note?.trim() || "" }, idempotencyKey);
      message.success("题库创建成功");
      setIsCreateModalOpen(false);
      bankForm.resetFields();
      await loadData();
    } catch (err: any) {
      if (err instanceof ApiError && err.code === "MAINTENANCE") {
        message.error("系统处于维护模式，禁止新建题库");
      } else {
        message.error(err.message || "创建题库失败");
      }
    } finally {
      setCreateSubmitting(false);
    }
  };

  const handleCreateSource = async (values: any) => {
    setSourceSubmitting(true);
    try {
      const idempotencyKey = getSecureRandomUUID();
      await createSource(
        {
          name: values.name.trim(),
          location: values.location.trim(),
          source_version: values.source_version.trim(),
          license: values.license.trim(),
          usage_scope: values.usage_scope.trim(),
        },
        idempotencyKey
      );
      message.success("来源创建成功");
      sourceForm.resetFields();
      await loadSources();
    } catch (err: any) {
      if (err instanceof ApiError && err.code === "MAINTENANCE") {
        message.error("系统处于维护模式，禁止创建来源");
      } else {
        message.error(err.message || "创建来源失败");
      }
    } finally {
      setSourceSubmitting(false);
    }
  };

  const filteredBanks = banks.filter(
    (b) =>
      b.name.toLowerCase().includes(searchText.toLowerCase()) ||
      (b.note && b.note.toLowerCase().includes(searchText.toLowerCase()))
  );

  return (
    <div style={{ maxWidth: 1100, margin: "0 auto", padding: "24px 0" }} data-testid="banks-view">
      {activeSession && (
        <Alert
          message="存在进行中的活动会话"
          description={`当前正在进行${activeSession.kind === "practice" ? "自适应练习" : "阶段考试"}会话。在活动会话完成前，题库内容与版本发现受防泄密保护。`}
          type="info"
          showIcon
          style={{ marginBottom: 20 }}
          action={
            <Button
              type="primary"
              size="small"
              onClick={() => {
                if (activeSession.kind === "practice") {
                  onNavigate?.("/practice");
                } else {
                  onNavigate?.(`/exams/${activeSession.id}`);
                }
              }}
              data-testid="continue-active-session-btn"
            >
              继续当前会话
            </Button>
          }
          data-testid="active-session-alert"
        />
      )}

      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", flexWrap: "wrap", gap: 12, marginBottom: 20 }}>
        <div style={{ flex: "1 1 240px", minWidth: 0 }}>
          <Title level={3} style={{ margin: 0 }}>
            题库管理
          </Title>
          <Text type="secondary">管理用于自适应练习与阶段考试的词汇题库、来源与版本</Text>
        </div>
        <Space wrap size={[8, 8]} style={{ maxWidth: "100%" }}>
          <Button
            icon={<FolderOpenOutlined />}
            onClick={() => {
              setIsSourceDrawerOpen(true);
              loadSources();
            }}
            data-testid="manage-sources-btn"
          >
            管理来源
          </Button>
          <Button
            type="primary"
            icon={<PlusOutlined />}
            onClick={() => setIsCreateModalOpen(true)}
            data-testid="create-bank-btn"
          >
            新建题库
          </Button>
        </Space>
      </div>

      <div style={{ marginBottom: 20 }}>
        <Input
          prefix={<SearchOutlined style={{ color: "#bfbfbf" }} />}
          placeholder="搜索题库名称或备注..."
          value={searchText}
          onChange={(e) => setSearchText(e.target.value)}
          allowClear
          style={{ maxWidth: 360 }}
          data-testid="search-bank-input"
        />
      </div>

      {banksError && !loading && (
        <Card style={{ marginBottom: 20 }}>
          <Alert
            message="题库加载失败"
            description={banksError}
            type="error"
            showIcon
            action={
              <Button type="primary" danger onClick={loadData} data-testid="retry-load-banks-btn">
                重试
              </Button>
            }
          />
        </Card>
      )}

      {!loading && !banksError && banks.length === 0 && (
        <Card style={{ textAlign: "center", padding: "40px 0" }}>
          <Empty
            description="暂无题库"
            image={Empty.PRESENTED_IMAGE_SIMPLE}
            data-testid="empty-banks"
          >
            <Paragraph type="secondary" style={{ marginBottom: 24 }}>
              系统当前没有任何题库。您可以新建一个题库，或者添加数据来源。
            </Paragraph>
            <Space>
              <Button
                type="primary"
                icon={<PlusOutlined />}
                onClick={() => setIsCreateModalOpen(true)}
                data-testid="empty-create-bank-btn"
              >
                新建题库
              </Button>
              <Button
                icon={<FolderOpenOutlined />}
                onClick={() => {
                  setIsSourceDrawerOpen(true);
                  loadSources();
                }}
                data-testid="empty-manage-sources-btn"
              >
                管理来源
              </Button>
            </Space>
          </Empty>
        </Card>
      )}

      {!loading && !banksError && banks.length > 0 && filteredBanks.length === 0 && (
        <Card style={{ textAlign: "center", padding: "32px 0" }} data-testid="no-matching-banks">
          <Empty description="没有匹配的题库" image={Empty.PRESENTED_IMAGE_SIMPLE}>
            <Button onClick={() => setSearchText("")} data-testid="clear-bank-search-btn">
              清除搜索
            </Button>
          </Empty>
        </Card>
      )}

      {!loading && !banksError && filteredBanks.length > 0 && <Row gutter={[16, 16]}>
        {filteredBanks.map((bank) => {
          const isArchived = bank.archived;
          return (
            <Col xs={24} sm={12} md={8} key={bank.id}>
              <Card
                hoverable
                style={{ height: "100%", display: "flex", flexDirection: "column" }}
                bodyStyle={{ flex: 1, display: "flex", flexDirection: "column" }}
                data-testid={`bank-card-${bank.id}`}
                actions={[
                  <Button
                    type="link"
                    key="detail"
                    onClick={() => onNavigate?.(`/banks/${bank.id}`)}
                    data-testid={`bank-detail-link-${bank.id}`}
                  >
                    详情
                  </Button>,
                  <Button
                    type="link"
                    key="practice"
                    disabled={isArchived}
                    icon={<PlayCircleOutlined />}
                    onClick={() => onNavigate?.(`/practice?bank_id=${bank.id}`)}
                    data-testid={`practice-btn-${bank.id}`}
                  >
                    练习
                  </Button>,
                  <Button
                    type="link"
                    key="exam"
                    disabled={isArchived}
                    icon={<FormOutlined />}
                    onClick={() => onNavigate?.(`/exams/new?bank_id=${bank.id}`)}
                    data-testid={`exam-btn-${bank.id}`}
                  >
                    考试
                  </Button>,
                ]}
              >
                <div style={{ display: "flex", justifyContent: "space-between", alignItems: "flex-start", marginBottom: 12 }}>
                  <Title level={4} style={{ margin: 0, wordBreak: "break-all" }}>
                    {bank.name}
                  </Title>
                  {isArchived ? (
                    <Tag color="default" data-testid={`bank-archived-tag-${bank.id}`}>
                      已归档
                    </Tag>
                  ) : (
                    <Tag color="success" data-testid={`bank-active-tag-${bank.id}`}>
                      正常
                    </Tag>
                  )}
                </div>

                <Paragraph
                  type="secondary"
                  ellipsis={{ rows: 2 }}
                  style={{ minHeight: 40, marginBottom: 16 }}
                >
                  {bank.note || "暂无备注"}
                </Paragraph>

                <div style={{ marginTop: "auto", display: "flex", justifyContent: "space-between" }}>
                  <Statistic
                    title="词义成员"
                    value={bank.member_count}
                    valueStyle={{ fontSize: 20 }}
                  />
                  <Statistic
                    title="修订版本"
                    value={`rev ${bank.revision}`}
                    valueStyle={{ fontSize: 16 }}
                  />
                </div>
              </Card>
            </Col>
          );
        })}
      </Row>}

      {/* Create Bank Modal */}
      <Modal
        title="新建题库"
        open={isCreateModalOpen}
        onCancel={() => {
          setIsCreateModalOpen(false);
          bankForm.resetFields();
        }}
        footer={null}
        data-testid="create-bank-modal"
      >
        <Form form={bankForm} layout="vertical" onFinish={handleCreateBank}>
          <Form.Item
            name="name"
            label="题库名称"
            rules={[
              { required: true, message: "请输入题库名称" },
              { max: 120, message: "题库名称不能超过120个字符" },
            ]}
          >
            <Input placeholder="例如：核心四级高频词汇" data-testid="bank-name-input" />
          </Form.Item>
          <Form.Item name="note" label="备注说明">
            <Input.TextArea rows={3} placeholder="选填，关于题库的补充说明" data-testid="bank-note-input" />
          </Form.Item>
          <Form.Item style={{ marginBottom: 0, textAlign: "right" }}>
            <Space>
              <Button onClick={() => setIsCreateModalOpen(false)}>取消</Button>
              <Button type="primary" htmlType="submit" loading={createSubmitting} data-testid="submit-bank-btn">
                创建
              </Button>
            </Space>
          </Form.Item>
        </Form>
      </Modal>

      {/* Sources Drawer */}
      <Drawer
        title="内容来源管理"
        width="min(520px, 100vw)"
        open={isSourceDrawerOpen}
        onClose={() => setIsSourceDrawerOpen(false)}
        data-testid="source-drawer"
      >
        <Title level={5}>已登记来源</Title>
        <Spin spinning={loadingSources}>
          {sourcesError ? (
            <Alert
              type="error"
              showIcon
              message="来源列表加载失败"
              description={sourcesError}
              action={<Button size="small" onClick={loadSources}>重试</Button>}
              style={{ marginBottom: 16 }}
              data-testid="sources-error-alert"
            />
          ) : sources.length === 0 ? (
            <Empty description="暂无来源记录" image={Empty.PRESENTED_IMAGE_SIMPLE} />
          ) : (
            <div style={{ marginBottom: 24 }}>
              {sources.map((src) => (
                <Card size="small" key={src.id} style={{ marginBottom: 8 }} data-testid={`source-card-${src.id}`}>
                  <Text strong>{src.name}</Text> <Tag color="blue">{src.source_version}</Tag>
                  <div style={{ fontSize: 12, color: "#8c8c8c", marginTop: 4 }}>
                    <div style={{ overflowWrap: "anywhere" }}>位置: {src.location}</div>
                    <div style={{ overflowWrap: "anywhere" }}>授权: {src.license} · 适用: {src.usage_scope}</div>
                  </div>
                </Card>
              ))}
            </div>
          )}
        </Spin>

        <Title level={5} style={{ marginTop: 20 }}>添加新来源</Title>
        <Form form={sourceForm} layout="vertical" onFinish={handleCreateSource}>
          <Form.Item
            name="name"
            label="来源名称"
            rules={[{ required: true, message: "请输入来源名称" }]}
          >
            <Input placeholder="例如：CET-4 Official Syllabus" data-testid="source-name-input" />
          </Form.Item>
          <Form.Item
            name="location"
            label="来源地址/路径"
            rules={[{ required: true, message: "请输入来源路径或URL" }]}
          >
            <Input placeholder="例如：https://example.com/vocab.json" data-testid="source-location-input" />
          </Form.Item>
          <Form.Item
            name="source_version"
            label="版本标识"
            rules={[{ required: true, message: "请输入版本标识" }]}
          >
            <Input placeholder="例如：2026.1" data-testid="source-version-input" />
          </Form.Item>
          <Form.Item
            name="license"
            label="许可协议"
            rules={[{ required: true, message: "请输入许可协议" }]}
          >
            <Input placeholder="例如：CC-BY-4.0" data-testid="source-license-input" />
          </Form.Item>
          <Form.Item
            name="usage_scope"
            label="使用范围"
            rules={[{ required: true, message: "请输入使用范围" }]}
          >
            <Input placeholder="例如：academic_use" data-testid="source-scope-input" />
          </Form.Item>
          <Form.Item style={{ textAlign: "right" }}>
            <Button type="primary" htmlType="submit" loading={sourceSubmitting} data-testid="submit-source-btn">
              添加来源
            </Button>
          </Form.Item>
        </Form>
      </Drawer>
    </div>
  );
};
