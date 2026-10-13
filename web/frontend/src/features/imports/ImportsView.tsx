import React, { useState, useEffect } from "react";
import {
  Card,
  Row,
  Col,
  Button,
  Select,
  Input,
  Tabs,
  Upload,
  Table,
  Tag,
  Space,
  Typography,
  Alert,
  message,
  Statistic,
  Drawer,
  Checkbox,
  Badge,
} from "antd";
import {
  UploadOutlined,
  FileTextOutlined,
  CheckCircleOutlined,
  ExclamationCircleOutlined,
  QuestionCircleOutlined,
  CloseCircleOutlined,
  ReloadOutlined,
  DatabaseOutlined,
  EditOutlined,
  StopOutlined,
} from "@ant-design/icons";
import { getSecureRandomUUID } from "../../utils/uuid";
import {
  fetchBanks,
  fetchBank,
  importBankVocabularyJson,
  importBankVocabularyFile,
  fetchImportJob,
  fetchImportItems,
  resolveImportItem,
  ApiError,
} from "./api";
import {
  BankOption,
  ImportJob,
  ImportItem,
  ImportItemStatus,
} from "./types";

const { Title, Text, Paragraph } = Typography;
const { TextArea } = Input;

export interface ImportsViewProps {
  onNavigate?: (path: string) => void;
}

const MAX_FILE_SIZE = 2 * 1024 * 1024; // 2 MiB
const MAX_ROWS = 5000;

export const ImportsView: React.FC<ImportsViewProps> = ({ onNavigate }) => {
  const [banks, setBanks] = useState<BankOption[]>([]);
  const [loadingBanks, setLoadingBanks] = useState<boolean>(true);
  const [selectedBankId, setSelectedBankId] = useState<string>("");
  const [selectedBank, setSelectedBank] = useState<BankOption | null>(null);

  // Input form state
  const [inputTab, setInputTab] = useState<string>("text");
  const [textInput, setTextInput] = useState<string>("");
  const [selectedFile, setSelectedFile] = useState<File | null>(null);
  const [fileValidationErr, setFileValidationErr] = useState<string | null>(null);
  const [textValidationErr, setTextValidationErr] = useState<string | null>(null);
  const [importing, setImporting] = useState<boolean>(false);
  const [serverError, setServerError] = useState<string | null>(null);
  const [lastOpKey, setLastOpKey] = useState<string>(getSecureRandomUUID());

  // Active import job state
  const [activeImportId, setActiveImportId] = useState<string | null>(null);
  const [importJob, setImportJob] = useState<ImportJob | null>(null);
  const [loadingJob, setLoadingJob] = useState<boolean>(false);

  // Items table state
  const [items, setItems] = useState<ImportItem[]>([]);
  const [loadingItems, setLoadingItems] = useState<boolean>(false);
  const [statusFilter, setStatusFilter] = useState<string>("all");
  const [page, setPage] = useState<number>(1);
  const [pageSize, setPageSize] = useState<number>(10);
  const [totalItemsCount, setTotalItemsCount] = useState<number>(0);
  const [counts, setCounts] = useState<Record<string, number>>({});

  // Disambiguation drawer state
  const [activeItem, setActiveItem] = useState<ImportItem | null>(null);
  const [isDrawerOpen, setIsDrawerOpen] = useState<boolean>(false);
  const [selectedSenseIds, setSelectedSenseIds] = useState<string[]>([]);
  const [resolveReason, setResolveReason] = useState<string>("");
  const [resolving, setResolving] = useState<boolean>(false);
  const [drawerError, setDrawerError] = useState<string | null>(null);
  const [resolveOpKey, setResolveOpKey] = useState<string>(getSecureRandomUUID());

  // Load banks list and parse initial URL params
  useEffect(() => {
    const searchParams = new URLSearchParams(window.location.search);
    const bankIdParam = searchParams.get("bank_id");
    const importIdParam = searchParams.get("import_id");

    if (bankIdParam) {
      setSelectedBankId(bankIdParam);
    }
    if (importIdParam) {
      setActiveImportId(importIdParam);
    }

    loadBanks(bankIdParam);
  }, []);

  const loadBanks = async (preferredBankId?: string | null) => {
    setLoadingBanks(true);
    try {
      const res = await fetchBanks();
      const activeBanks = (res.items || []).filter((b) => !b.archived);
      setBanks(activeBanks);
      if (preferredBankId && activeBanks.some((b) => b.id === preferredBankId)) {
        setSelectedBankId(preferredBankId);
      } else if (activeBanks.length > 0) {
        setSelectedBankId((prev) => prev || activeBanks[0].id);
      }
    } catch (err: any) {
      message.error("加载题库列表失败");
    } finally {
      setLoadingBanks(false);
    }
  };

  // Sync selected bank details
  useEffect(() => {
    if (!selectedBankId) {
      setSelectedBank(null);
      return;
    }
    fetchBank(selectedBankId)
      .then((res) => setSelectedBank(res.bank))
      .catch(() => {});
  }, [selectedBankId]);

  // Load active import job and items
  useEffect(() => {
    if (!activeImportId) {
      setImportJob(null);
      setItems([]);
      return;
    }
    loadJobDetails(activeImportId);
  }, [activeImportId]);

  // Reload items when filter or pagination changes
  useEffect(() => {
    if (!activeImportId) return;
    loadItems(activeImportId, statusFilter, page, pageSize);
  }, [activeImportId, statusFilter, page, pageSize]);

  const loadJobDetails = async (jobId: string) => {
    setLoadingJob(true);
    try {
      const job = await fetchImportJob(jobId);
      setImportJob(job);
      if (job.target_id) {
        setSelectedBankId(job.target_id);
      }
    } catch (err: any) {
      // Fallback: items call also provides total and counts
    } finally {
      setLoadingJob(false);
    }
  };

  const loadItems = async (jobId: string, filter: string, currentPage: number, size: number) => {
    setLoadingItems(true);
    try {
      const offset = (currentPage - 1) * size;
      const res = await fetchImportItems(jobId, filter, size, offset);
      setItems(res.items || []);
      setCounts(res.counts || {});

      // Calculate total count for this filter view
      if (filter === "all") {
        setTotalItemsCount(res.total);
      } else {
        setTotalItemsCount(res.counts?.[filter] || 0);
      }
    } catch (err: any) {
      message.error("加载导入条目失败");
    } finally {
      setLoadingItems(false);
    }
  };

  // Text input line count validation
  const handleTextChange = (e: React.ChangeEvent<HTMLTextAreaElement>) => {
    const val = e.target.value;
    setTextInput(val);
    setServerError(null);

    const lines = val.split("\n").filter((l) => l.trim().length > 0);
    if (lines.length > MAX_ROWS) {
      setTextValidationErr(`条目数量 (${lines.length}) 超过 5000 行限制`);
    } else {
      setTextValidationErr(null);
    }
  };

  // File selection validation
  const handleFileSelect = (file: File) => {
    setSelectedFile(file);
    setServerError(null);

    if (file.size > MAX_FILE_SIZE) {
      setFileValidationErr(`文件大小 (${(file.size / (1024 * 1024)).toFixed(2)} MiB) 超过 2 MiB 限制`);
    } else {
      setFileValidationErr(null);
    }
    return false; // prevent AntD auto upload
  };

  // Handle Import submission
  const handleSubmitImport = async () => {
    if (!selectedBankId) {
      message.warning("请选择目标题库");
      return;
    }
    if (!selectedBank) {
      message.warning("正在获取题库版本信息，请稍候");
      return;
    }

    setServerError(null);

    // Client-side validations
    if (inputTab === "text") {
      const trimmed = textInput.trim();
      if (!trimmed) {
        message.warning("请输入要导入的词汇条目");
        return;
      }
      const lines = trimmed.split("\n").filter((l) => l.trim().length > 0);
      if (lines.length > MAX_ROWS) {
        setServerError("条目数量超过 5000 行限制，请精简后重试");
        return;
      }

      setImporting(true);
      try {
        // Check if text is JSON array
        let wordsPayload: string[] | undefined;
        let textPayload: string | undefined = trimmed;
        if (trimmed.startsWith("[") && trimmed.endsWith("]")) {
          try {
            const parsed = JSON.parse(trimmed);
            if (Array.isArray(parsed) && parsed.every((x) => typeof x === "string")) {
              wordsPayload = parsed;
              textPayload = undefined;
            }
          } catch {
            // Keep textPayload as is
          }
        }

        const res = await importBankVocabularyJson(
          selectedBankId,
          {
            words: wordsPayload,
            text: textPayload,
            expected_revision: selectedBank.revision,
            resolution_policy_version: "v1",
          },
          lastOpKey
        );

        message.success("词表导入成功！");
        setLastOpKey(getSecureRandomUUID());
        setActiveImportId(res.import_id);
        setPage(1);
        setStatusFilter("all");
      } catch (err: any) {
        handleApiError(err);
      } finally {
        setImporting(false);
      }
    } else {
      // File upload
      if (!selectedFile) {
        message.warning("请选择要上传的词表文件");
        return;
      }
      if (selectedFile.size > MAX_FILE_SIZE) {
        setServerError("文件大小超过 2 MiB 限制，无法上传");
        return;
      }

      setImporting(true);
      try {
        const res = await importBankVocabularyFile(
          selectedBankId,
          selectedFile,
          selectedBank.revision,
          "v1",
          lastOpKey
        );

        message.success("词表文件上传并导入成功！");
        setLastOpKey(getSecureRandomUUID());
        setActiveImportId(res.import_id);
        setPage(1);
        setStatusFilter("all");
      } catch (err: any) {
        handleApiError(err);
      } finally {
        setImporting(false);
      }
    }
  };

  const handleApiError = (err: any) => {
    if (err instanceof ApiError) {
      if (err.code === "REVISION_CONFLICT") {
        setServerError("题库版本发生变化 (409 Conflict)，已自动刷新，请再次点击确认导入");
        if (selectedBankId) {
          fetchBank(selectedBankId).then((r) => setSelectedBank(r.bank)).catch(() => {});
        }
      } else if (err.code === "SESSION_ACTIVE") {
        setServerError("当前系统存在正在进行的学习或考试会话，题库处于锁定状态，禁止导入");
      } else if (err.code === "BANK_ARCHIVED") {
        setServerError("该题库已归档，无法继续导入词汇");
      } else if (err.code === "INVALID_REQUEST") {
        setServerError(`输入格式校验未通过: ${err.message}`);
      } else {
        setServerError(err.message || "导入失败");
      }
    } else {
      setServerError(err?.message || "网络请求失败，请检查连接后重试");
    }
  };

  // Disambiguation Drawer open
  const openResolveDrawer = (item: ImportItem) => {
    setActiveItem(item);
    setSelectedSenseIds(item.resolved_sense_ids || []);
    setResolveReason(item.disposition_reason || "");
    setDrawerError(null);
    setResolveOpKey(getSecureRandomUUID());
    setIsDrawerOpen(true);
  };

  // Perform Disambiguation Resolve (Adopt or Exclude)
  const handleResolveAction = async (action: "adopt" | "exclude") => {
    if (!activeItem) return;

    if (action === "adopt" && selectedSenseIds.length === 0) {
      message.warning("请至少选择一个要采纳的词义");
      return;
    }

    setResolving(true);
    setDrawerError(null);

    try {
      await resolveImportItem(
        activeItem.id,
        {
          action,
          sense_refs: action === "adopt" ? selectedSenseIds : [],
          reason: resolveReason.trim() || (action === "adopt" ? "人工采纳词义" : "人工排除词条"),
          expected_revision: activeItem.revision,
        },
        resolveOpKey
      );

      message.success(action === "adopt" ? "已成功采纳所选词义" : "已将该条目标记为排除");
      setIsDrawerOpen(false);
      setActiveItem(null);

      // Refresh job and items list
      if (activeImportId) {
        loadJobDetails(activeImportId);
        loadItems(activeImportId, statusFilter, page, pageSize);
      }
      // Refresh bank details
      if (selectedBankId) {
        fetchBank(selectedBankId).then((r) => setSelectedBank(r.bank)).catch(() => {});
      }
    } catch (err: any) {
      if (err instanceof ApiError && err.code === "REVISION_CONFLICT") {
        setDrawerError("条目版本冲突 (409)，该条目已被修改，请关闭重试");
      } else if (err instanceof ApiError && err.code === "SESSION_ACTIVE") {
        setDrawerError("存在正在进行的学习会话，题库成员已被写保护");
      } else {
        setDrawerError(err.message || "处置操作失败");
      }
    } finally {
      setResolving(false);
    }
  };

  const getStatusBadge = (status: ImportItemStatus) => {
    switch (status) {
      case "resolved":
        return <Tag color="success" icon={<CheckCircleOutlined />}>已采纳</Tag>;
      case "ambiguous":
        return <Tag color="warning" icon={<ExclamationCircleOutlined />}>待消歧</Tag>;
      case "not_found":
        return <Tag color="default" icon={<QuestionCircleOutlined />}>未找到</Tag>;
      case "excluded":
        return <Tag color="error" icon={<StopOutlined />}>已排除</Tag>;
      case "invalid":
        return <Tag color="error" icon={<CloseCircleOutlined />}>格式无效</Tag>;
      default:
        return <Tag>{status}</Tag>;
    }
  };

  // Table Columns
  const columns = [
    {
      title: "序号",
      dataIndex: "ordinal",
      key: "ordinal",
      width: 68,
      render: (ord: number) => <Text type="secondary">{ord + 1}</Text>,
    },
    {
      title: "输入词形",
      dataIndex: "raw_input",
      key: "raw_input",
      width: 140,
      render: (text: string) => <Text strong>{text}</Text>,
    },
    {
      title: "标准化",
      dataIndex: "normalized_word",
      key: "normalized_word",
      width: 120,
      render: (text: string) => <Text code>{text || "-"}</Text>,
    },
    {
      title: "状态",
      dataIndex: "status",
      key: "status",
      width: 110,
      render: (st: ImportItemStatus) => getStatusBadge(st),
    },
    {
      title: "候选词义",
      key: "candidates",
      width: 220,
      render: (_: any, record: ImportItem) => {
        if (!record.candidates || record.candidates.length === 0) {
          return <Text type="secondary">无候选</Text>;
        }
        return (
          <Space direction="vertical" size={2} style={{ maxWidth: 260 }}>
            {record.candidates.map((c, idx) => (
              <Text key={c.sense_id || idx} style={{ fontSize: 13 }} ellipsis>
                <Tag color="blue">{c.part_of_speech}</Tag> {c.meaning}
              </Text>
            ))}
          </Space>
        );
      },
    },
    {
      title: "处置说明 / 采纳详情",
      key: "disposition",
      ellipsis: true,
      render: (_: any, record: ImportItem) => {
        if (record.status === "resolved" && record.resolved_sense_ids?.length > 0) {
          return (
            <Text type="success" style={{ fontSize: 13 }}>
              已采纳 {record.resolved_sense_ids.length} 个词义
              {record.disposition_reason && ` (${record.disposition_reason})`}
            </Text>
          );
        }
        return (
          <Text type="secondary" style={{ fontSize: 13 }}>
            {record.disposition_reason || "-"}
          </Text>
        );
      },
    },
    {
      title: "操作",
      key: "actions",
      width: 100,
      render: (_: any, record: ImportItem) => {
        const canResolve = record.status === "ambiguous" || record.status === "not_found";
        return (
          <Button
            type={record.status === "ambiguous" ? "primary" : "default"}
            size="small"
            disabled={!canResolve}
            onClick={() => openResolveDrawer(record)}
            data-testid={`resolve-btn-${record.ordinal}`}
          >
            {record.status === "ambiguous" ? "消歧" : "处理"}
          </Button>
        );
      },
    },
  ];

  return (
    <div style={{ maxWidth: 1100, margin: "0 auto", padding: "24px 0" }} data-testid="imports-view">
      {/* Top Header */}
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 20 }}>
        <Space align="center">
          <Title level={3} style={{ margin: 0 }}>
            词表导入与消歧
          </Title>
          {selectedBank && (
            <Tag color="blue" data-testid="active-bank-tag">
              目标题库: {selectedBank.name} (rev {selectedBank.revision}, {selectedBank.member_count} 词)
            </Tag>
          )}
        </Space>

        <Space>
          {activeImportId ? (
            <>
              <Button
                icon={<ReloadOutlined />}
                onClick={() => {
                  loadJobDetails(activeImportId);
                  loadItems(activeImportId, statusFilter, page, pageSize);
                }}
              >
                刷新
              </Button>
              {selectedBankId && (
                <Button
                  icon={<DatabaseOutlined />}
                  onClick={() => onNavigate?.(`/banks/${selectedBankId}`)}
                  data-testid="view-bank-btn"
                >
                  查看题库
                </Button>
              )}
              <Button
                type="primary"
                icon={<UploadOutlined />}
                onClick={() => {
                  setActiveImportId(null);
                  setImportJob(null);
                  setItems([]);
                }}
                data-testid="new-import-btn"
              >
                新建导入
              </Button>
            </>
          ) : (
            selectedBankId && (
              <Button
                icon={<DatabaseOutlined />}
                onClick={() => onNavigate?.(`/banks/${selectedBankId}`)}
              >
                返回题库
              </Button>
            )
          )}
        </Space>
      </div>

      {/* VIEW 1: Import Upload Form (when no active import) */}
      {!activeImportId && (
        <Card style={{ marginBottom: 24 }} data-testid="import-create-card">
          <Row gutter={24}>
            <Col xs={24} md={14}>
              <div style={{ marginBottom: 16 }}>
                <Text strong style={{ display: "block", marginBottom: 8 }}>
                  1. 选择目标题库 <Text type="danger">*</Text>
                </Text>
                <Select
                  style={{ width: "100%" }}
                  placeholder="请选择要导入的题库"
                  value={selectedBankId || undefined}
                  onChange={(val) => setSelectedBankId(val)}
                  loading={loadingBanks}
                  data-testid="bank-select"
                >
                  {banks.map((b) => (
                    <Select.Option key={b.id} value={b.id}>
                      {b.name} (当前包含 {b.member_count} 词义, rev {b.revision})
                    </Select.Option>
                  ))}
                </Select>
              </div>

              <div style={{ marginBottom: 16 }}>
                <Text strong style={{ display: "block", marginBottom: 8 }}>
                  2. 输入或上传词表内容 <Text type="danger">*</Text>
                </Text>

                <Tabs
                  activeKey={inputTab}
                  onChange={(key) => {
                    setInputTab(key);
                    setServerError(null);
                  }}
                  data-testid="import-mode-tabs"
                  items={[
                    {
                      key: "text",
                      label: (
                        <span>
                          <FileTextOutlined /> 文本 / 粘贴输入
                        </span>
                      ),
                      children: (
                        <div>
                          <TextArea
                            rows={8}
                            placeholder="一行一个词（或英文逗号分隔），也可直接粘贴 JSON 数组，例如 [&quot;apple&quot;, &quot;river&quot;]&#10;river&#10;mountain&#10;bank"
                            value={textInput}
                            onChange={handleTextChange}
                            data-testid="import-text-input"
                          />
                          <div style={{ display: "flex", justifyContent: "space-between", marginTop: 6 }}>
                            <Text type="secondary" style={{ fontSize: 12 }}>
                              支持纯文本与 JSON 格式。最多 5000 行。
                            </Text>
                            <Text
                              type={textValidationErr ? "danger" : "secondary"}
                              style={{ fontSize: 12 }}
                            >
                              当前: {textInput.split("\n").filter((l) => l.trim().length > 0).length} / 5000 行
                            </Text>
                          </div>
                          {textValidationErr && (
                            <Alert
                              message={textValidationErr}
                              type="error"
                              showIcon
                              style={{ marginTop: 8 }}
                              data-testid="row-count-error"
                            />
                          )}
                        </div>
                      ),
                    },
                    {
                      key: "file",
                      label: (
                        <span>
                          <UploadOutlined /> 文件上传 (.txt, .csv, .json)
                        </span>
                      ),
                      children: (
                        <div>
                          <div data-testid="import-file-upload">
                            <Upload.Dragger
                              name="file"
                              multiple={false}
                              accept=".txt,.csv,.json,text/plain,text/csv,application/json"
                              beforeUpload={handleFileSelect}
                              showUploadList={false}
                            >
                              <p className="ant-upload-drag-icon">
                                <UploadOutlined style={{ fontSize: 32, color: "#1677ff" }} />
                              </p>
                              <p className="ant-upload-text">点击或将词表文件拖拽至此区域</p>
                              <p className="ant-upload-hint">
                                支持 UTF-8 编码的 .txt, .csv, .json 文件，单文件大小不超过 2 MiB，行数不超过 5000 行。
                              </p>
                            </Upload.Dragger>
                          </div>

                          {selectedFile && (
                            <div style={{ marginTop: 12, padding: "8px 12px", background: "#fafafa", borderRadius: 4 }}>
                              <Space>
                                <FileTextOutlined />
                                <Text strong>{selectedFile.name}</Text>
                                <Text type="secondary">({(selectedFile.size / 1024).toFixed(1)} KB)</Text>
                              </Space>
                            </div>
                          )}

                          {fileValidationErr && (
                            <Alert
                              message={fileValidationErr}
                              type="error"
                              showIcon
                              style={{ marginTop: 12 }}
                              data-testid="file-size-error"
                            />
                          )}
                        </div>
                      ),
                    },
                  ]}
                />
              </div>

              {serverError && (
                <Alert
                  message={serverError}
                  type="error"
                  showIcon
                  style={{ marginBottom: 16 }}
                  data-testid="server-error-alert"
                />
              )}

              <Button
                type="primary"
                size="large"
                icon={<UploadOutlined />}
                loading={importing}
                onClick={handleSubmitImport}
                disabled={Boolean(textValidationErr || fileValidationErr)}
                data-testid="submit-import-btn"
                style={{ width: "100%", marginTop: 8 }}
              >
                确认导入并解析词义
              </Button>
            </Col>

            <Col xs={24} md={10}>
              <Card title="导入与消歧说明" size="small" style={{ backgroundColor: "#fafafa" }}>
                <Paragraph style={{ fontSize: 13 }}>
                  • <strong>自动精准匹配</strong>：词库中存在唯一规范词义的词条会自动采纳并入库。
                </Paragraph>
                <Paragraph style={{ fontSize: 13 }}>
                  • <strong>歧义人工消歧</strong>：一词多义项（例如 <em>bank</em> 可为“银行”或“河岸”）会被标记为待消歧，支持人工批量采纳多个词义或整体排除。
                </Paragraph>
                <Paragraph style={{ fontSize: 13 }}>
                  • <strong>未收录词条</strong>：当前词典快照未收录的词汇可统一排除，不影响正常词条入库。
                </Paragraph>
                <Paragraph style={{ fontSize: 13 }}>
                  • <strong>严格边界保护</strong>：单次导入上限 <strong>2 MiB</strong> 与 <strong>5000 行</strong>；活动作答期间受 CAS 与写隔离保护。
                </Paragraph>
              </Card>
            </Col>
          </Row>
        </Card>
      )}

      {/* VIEW 2: Active Import Task Summary & Items Table */}
      {activeImportId && (
        <>
          {/* Summary Card */}
          <Card style={{ marginBottom: 24 }} loading={loadingJob} data-testid="import-summary-card">
            <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 16 }}>
              <div>
                <Space align="baseline">
                  <Title level={4} style={{ margin: 0 }}>
                    导入任务汇总
                  </Title>
                  <Text type="secondary" code>
                    {activeImportId}
                  </Text>
                </Space>
                <Paragraph type="secondary" style={{ margin: "4px 0 0 0", fontSize: 13 }}>
                  汇总数据基于任务实际全部条目统计（非当前分页）；消歧与采纳结果将实时同步至题库。
                </Paragraph>
              </div>

              {selectedBank && (
                <div style={{ textAlign: "right" }}>
                  <Text type="secondary" style={{ fontSize: 12 }}>题库当前版本</Text>
                  <div>
                    <Tag color="blue">rev {selectedBank.revision}</Tag>
                    <Tag color="green">{selectedBank.member_count} 词义成员</Tag>
                  </div>
                </div>
              )}
            </div>

            <Row gutter={[16, 16]}>
              <Col xs={12} sm={8} md={4}>
                <Statistic
                  title="总条目数"
                  value={importJob ? importJob.total_count : (counts.resolved ?? 0) + (counts.ambiguous ?? 0) + (counts.not_found ?? 0) + (counts.excluded ?? 0) + (counts.invalid ?? 0)}
                  data-testid="stat-total"
                />
              </Col>
              <Col xs={12} sm={8} md={4}>
                <Statistic
                  title="已采纳入库"
                  value={importJob ? importJob.resolved_count : counts.resolved || 0}
                  valueStyle={{ color: "#3f8600" }}
                  data-testid="stat-resolved"
                />
              </Col>
              <Col xs={12} sm={8} md={4}>
                <Statistic
                  title="待人工消歧"
                  value={importJob ? importJob.ambiguous_count : counts.ambiguous || 0}
                  valueStyle={{ color: (importJob?.ambiguous_count || counts.ambiguous) ? "#d48806" : undefined }}
                  data-testid="stat-ambiguous"
                />
              </Col>
              <Col xs={12} sm={8} md={4}>
                <Statistic
                  title="词典未找到"
                  value={importJob ? importJob.not_found_count : counts.not_found || 0}
                  data-testid="stat-not-found"
                />
              </Col>
              <Col xs={12} sm={8} md={4}>
                <Statistic
                  title="已人工排除"
                  value={importJob ? importJob.excluded_count : counts.excluded || 0}
                  data-testid="stat-excluded"
                />
              </Col>
              <Col xs={12} sm={8} md={4}>
                <Statistic
                  title="格式无效"
                  value={importJob ? importJob.invalid_count : counts.invalid || 0}
                  valueStyle={{ color: (importJob?.invalid_count || counts.invalid) ? "#cf1322" : undefined }}
                  data-testid="stat-invalid"
                />
              </Col>
            </Row>
          </Card>

          {/* Items Filter and Table */}
          <Card data-testid="import-items-card">
            <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 16 }}>
              <Tabs
                activeKey={statusFilter}
                onChange={(key) => {
                  setStatusFilter(key);
                  setPage(1);
                }}
                data-testid="status-filter"
                items={[
                  { key: "all", label: `全部条目` },
                  {
                    key: "ambiguous",
                    label: (
                      <span>
                        待消歧
                        {(counts.ambiguous || 0) > 0 && (
                          <Badge
                            count={counts.ambiguous}
                            overflowCount={9999}
                            style={{ marginLeft: 6, backgroundColor: "#faad14" }}
                          />
                        )}
                      </span>
                    ),
                  },
                  { key: "resolved", label: `已采纳 (${counts.resolved || 0})` },
                  { key: "not_found", label: `未收录 (${counts.not_found || 0})` },
                  { key: "excluded", label: `已排除 (${counts.excluded || 0})` },
                  { key: "invalid", label: `无效 (${counts.invalid || 0})` },
                ]}
              />

              <Button
                icon={<ReloadOutlined />}
                size="small"
                onClick={() => loadItems(activeImportId, statusFilter, page, pageSize)}
              >
                刷新列表
              </Button>
            </div>

            <Table
              rowKey="id"
              dataSource={items}
              columns={columns}
              loading={loadingItems}
              pagination={{
                current: page,
                pageSize: pageSize,
                total: totalItemsCount,
                showSizeChanger: true,
                pageSizeOptions: ["10", "20", "50"],
                onChange: (p, ps) => {
                  setPage(p);
                  setPageSize(ps);
                },
                showTotal: (total, range) => `第 ${range[0]}–${range[1]} 项，共 ${total} 项`,
              }}
              data-testid="import-items-table"
            />
          </Card>
        </>
      )}

      {/* VIEW 3: Disambiguation Drawer */}
      <Drawer
        title={
          activeItem ? (
            <Space>
              <EditOutlined />
              <span>
                词条消歧处置: <Text strong>{activeItem.normalized_word}</Text>
              </span>
            </Space>
          ) : (
            "消歧处置"
          )
        }
        width={560}
        open={isDrawerOpen}
        onClose={() => {
          setIsDrawerOpen(false);
          setActiveItem(null);
        }}
        data-testid="resolve-drawer"
        footer={
          <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
            <Button
              danger
              icon={<StopOutlined />}
              loading={resolving}
              onClick={() => handleResolveAction("exclude")}
              data-testid="exclude-btn"
            >
              排除此词条
            </Button>
            <Space>
              <Button onClick={() => setIsDrawerOpen(false)}>取消</Button>
              <Button
                type="primary"
                icon={<CheckCircleOutlined />}
                loading={resolving}
                disabled={selectedSenseIds.length === 0}
                onClick={() => handleResolveAction("adopt")}
                data-testid="adopt-btn"
              >
                采纳所选词义 ({selectedSenseIds.length})
              </Button>
            </Space>
          </div>
        }
      >
        {activeItem && (
          <div>
            <div style={{ marginBottom: 20 }}>
              <Paragraph>
                <strong>原始输入：</strong> <Text code>{activeItem.raw_input}</Text>
              </Paragraph>
              <Paragraph>
                <strong>当前状态：</strong> {getStatusBadge(activeItem.status)}
                <span style={{ marginLeft: 12 }}>
                  <Text type="secondary">版本号: rev {activeItem.revision}</Text>
                </span>
              </Paragraph>
              {activeItem.disposition_reason && (
                <Paragraph type="secondary" style={{ fontSize: 13 }}>
                  原因说明: {activeItem.disposition_reason}
                </Paragraph>
              )}
            </div>

            {drawerError && (
              <Alert
                message={drawerError}
                type="error"
                showIcon
                style={{ marginBottom: 16 }}
                data-testid="drawer-error-alert"
              />
            )}

            <div style={{ marginBottom: 24 }}>
              <Text strong style={{ display: "block", marginBottom: 12 }}>
                候选词义列表（可勾选一个或多个词义同时采纳）：
              </Text>

              {activeItem.candidates && activeItem.candidates.length > 0 ? (
                <Checkbox.Group
                  style={{ width: "100%" }}
                  value={selectedSenseIds}
                  onChange={(vals) => setSelectedSenseIds(vals as string[])}
                  data-testid="candidate-checkbox-group"
                >
                  <Space direction="vertical" style={{ width: "100%" }}>
                    {activeItem.candidates.map((cand) => (
                      <Card
                        key={cand.sense_id}
                        size="small"
                        hoverable
                        style={{
                          marginBottom: 8,
                          border: selectedSenseIds.includes(cand.sense_id)
                            ? "1px solid #1677ff"
                            : "1px solid #f0f0f0",
                          backgroundColor: selectedSenseIds.includes(cand.sense_id)
                            ? "#e6f4ff"
                            : "#ffffff",
                        }}
                      >
                        <Checkbox value={cand.sense_id} data-testid={`candidate-checkbox-${cand.sense_id}`}>
                          <Space align="start">
                            <Tag color="blue">{cand.part_of_speech}</Tag>
                            <div>
                              <Text strong style={{ fontSize: 14 }}>
                                {cand.meaning}
                              </Text>
                              <div>
                                <Text type="secondary" style={{ fontSize: 11 }}>
                                  Sense ID: {cand.sense_id}
                                </Text>
                              </div>
                            </div>
                          </Space>
                        </Checkbox>
                      </Card>
                    ))}
                  </Space>
                </Checkbox.Group>
              ) : (
                <Alert
                  message="词库中未找到标准候选词义"
                  description="若当前词典暂未收录该单词，建议将其标记为排除，或在添加新来源后重新导入。"
                  type="info"
                  showIcon
                />
              )}
            </div>

            <div style={{ marginTop: 16 }}>
              <Text strong style={{ display: "block", marginBottom: 8 }}>
                人工处置备注 (可选)
              </Text>
              <Input
                placeholder="例如：人工确认语义、多义词同时学习"
                value={resolveReason}
                onChange={(e) => setResolveReason(e.target.value)}
                data-testid="resolve-reason-input"
              />
            </div>
          </div>
        )}
      </Drawer>
    </div>
  );
};
