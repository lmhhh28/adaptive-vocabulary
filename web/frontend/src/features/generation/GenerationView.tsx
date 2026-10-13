import React, { useState, useEffect, useRef } from "react";
import {
  Card,
  Button,
  Select,
  Table,
  Tag,
  Space,
  Typography,
  Alert,
  message,
  Drawer,
  Modal,
  Tabs,
  Descriptions,
  Spin,
} from "antd";
import {
  ThunderboltOutlined,
  PlusOutlined,
  ReloadOutlined,
  CheckCircleOutlined,
  LockOutlined,
  FileSearchOutlined,
} from "@ant-design/icons";
import {
  listGenerationJobs,
  getGenerationJob,
  createGenerationJob,
  getCandidateDetail,
  listQualityFindings,
  listBanks,
  listSenseVersions,
} from "./api";
import {
  JobSummary,
  QualityFindingItem,
  BankOption,
  SenseVersionItem,
  CandidateDetailResponse,
} from "./types";

const { Title, Text, Paragraph } = Typography;

export interface GenerationViewProps {
  onNavigate?: (path: string) => void;
}

export const GenerationView: React.FC<GenerationViewProps> = ({ onNavigate: _onNavigate }) => {
  const [activeTab, setActiveTab] = useState<string>("jobs");

  // Jobs state
  const [jobs, setJobs] = useState<JobSummary[]>([]);
  const [jobsLoading, setJobsLoading] = useState<boolean>(false);
  const [jobsCursor, setJobsCursor] = useState<string | undefined>(undefined);
  const [jobsCursorStack, setJobsCursorStack] = useState<(string | undefined)[]>([]);
  const [jobsNextCursor, setJobsNextCursor] = useState<string | null>(null);
  const [jobsHasMore, setJobsHasMore] = useState<boolean>(false);

  // Create Job state
  const [banks, setBanks] = useState<BankOption[]>([]);
  const [selectedBankId, setSelectedBankId] = useState<string | undefined>(undefined);
  const [senses, setSenses] = useState<SenseVersionItem[]>([]);
  const [sensesLoading, setSensesLoading] = useState<boolean>(false);
  const [selectedSenseKeys, setSelectedSenseKeys] = useState<React.Key[]>([]);
  const [creatingJob, setCreatingJob] = useState<boolean>(false);
  const [createdJob, setCreatedJob] = useState<{ job_id: string; status: string } | null>(null);

  // Job Detail Drawer state
  const [selectedJobId, setSelectedJobId] = useState<string | null>(null);
  const [jobDetail, setJobDetail] = useState<JobSummary | null>(null);
  const [jobDetailLoading, setJobDetailLoading] = useState<boolean>(false);

  // Candidate Detail Modal state
  const [inspectingCandidateId, setInspectingCandidateId] = useState<string | null>(null);
  const [candidateDetail, setCandidateDetail] = useState<CandidateDetailResponse | null>(null);
  const [candidateLoading, setCandidateLoading] = useState<boolean>(false);
  const [candidateSessionLocked, setCandidateSessionLocked] = useState<boolean>(false);

  // Quality Findings state
  const [findings, setFindings] = useState<QualityFindingItem[]>([]);
  const [findingsLoading, setFindingsLoading] = useState<boolean>(false);
  const [findingStatusFilter, setFindingStatusFilter] = useState<string>("");
  const [findingsCursor, setFindingsCursor] = useState<string | undefined>(undefined);
  const [findingsCursorStack, setFindingsCursorStack] = useState<(string | undefined)[]>([]);
  const [findingsNextCursor, setFindingsNextCursor] = useState<string | null>(null);
  const [findingsHasMore, setFindingsHasMore] = useState<boolean>(false);
  const jobsRequestRef = useRef<number>(0);
  const findingsRequestRef = useRef<number>(0);

  // Load Banks on mount
  useEffect(() => {
    listBanks()
      .then((res) => setBanks(res.items || []))
      .catch((err) => message.error("加载题库列表失败: " + err.message));
  }, []);

  // Load Jobs
  const loadJobs = (cursor?: string) => {
    const requestId = ++jobsRequestRef.current;
    setJobsLoading(true);
    listGenerationJobs({ cursor, limit: 10 })
      .then((res) => {
        if (jobsRequestRef.current !== requestId) return;
        setJobs(res.items || []);
        setJobsNextCursor(res.next_cursor || null);
        setJobsHasMore(res.has_more || false);
      })
      .catch((err) => {
        if (jobsRequestRef.current !== requestId) return;
        message.error("加载生成任务失败: " + err.message);
      })
      .finally(() => {
        if (jobsRequestRef.current === requestId) setJobsLoading(false);
      });
  };

  useEffect(() => {
    if (activeTab === "jobs") {
      loadJobs(jobsCursor);
    }
  }, [activeTab, jobsCursor]);

  // Load Senses when bank is selected
  useEffect(() => {
    if (!selectedBankId) {
      setSenses([]);
      setSelectedSenseKeys([]);
      return;
    }
    let cancelled = false;
    setSensesLoading(true);
    const fetchAllSenses = async () => {
      try {
        let allItems: any[] = [];
        let cursor: string | undefined = undefined;
        const seenCursors = new Set<string>();
        const seenIds = new Set<string>();
        while (!cancelled) {
          const res = await listSenseVersions({ bank_id: selectedBankId, limit: 100, cursor });
          const items = res.items || [];
          for (const item of items) {
            if (!seenIds.has(item.sense_version_id)) {
              seenIds.add(item.sense_version_id);
              allItems.push(item);
            }
          }
          if (!res.has_more || !res.next_cursor || items.length === 0) {
            break;
          }
          if (seenCursors.has(res.next_cursor)) {
            throw new Error("词义版本分页游标重复，已停止加载以避免循环");
          }
          seenCursors.add(res.next_cursor);
          cursor = res.next_cursor;
        }
        if (!cancelled) {
          setSenses(allItems);
          setSelectedSenseKeys([]);
        }
      } catch (err: any) {
        if (!cancelled) {
          message.error("加载词义版本失败: " + err.message);
        }
      } finally {
        if (!cancelled) {
          setSensesLoading(false);
        }
      }
    };
    fetchAllSenses();
    return () => {
      cancelled = true;
    };
  }, [selectedBankId]);

  // Load Findings
  const loadFindings = (cursor?: string, status?: string) => {
    const requestId = ++findingsRequestRef.current;
    setFindingsLoading(true);
    listQualityFindings({ cursor, status: status || undefined, limit: 10 })
      .then((res) => {
        if (findingsRequestRef.current !== requestId) return;
        setFindings(res.items || []);
        setFindingsNextCursor(res.next_cursor || null);
        setFindingsHasMore(res.has_more || false);
      })
      .catch((err) => {
        if (findingsRequestRef.current !== requestId) return;
        message.error("加载质检异常列表失败: " + err.message);
      })
      .finally(() => {
        if (findingsRequestRef.current === requestId) setFindingsLoading(false);
      });
  };

  useEffect(() => {
    if (activeTab === "findings") {
      loadFindings(findingsCursor, findingStatusFilter);
    }
  }, [activeTab, findingsCursor, findingStatusFilter]);

  // Open Job Detail
  const handleOpenJobDetail = (jobId: string) => {
    setSelectedJobId(jobId);
    setJobDetailLoading(true);
    getGenerationJob(jobId)
      .then((job) => {
        setJobDetail(job);
      })
      .catch((err) => {
        message.error("获取任务详情失败: " + err.message);
      })
      .finally(() => setJobDetailLoading(false));
  };

  // Inspect Candidate
  const handleInspectCandidate = (candidateId: string) => {
    setInspectingCandidateId(candidateId);
    setCandidateDetail(null);
    setCandidateSessionLocked(false);
    setCandidateLoading(true);

    getCandidateDetail(candidateId)
      .then((data) => {
        setCandidateDetail(data);
      })
      .catch((err: any) => {
        if (err?.status === 409 || err?.code === "SESSION_ACTIVE") {
          setCandidateSessionLocked(true);
        } else {
          message.error("获取候选详情失败: " + (err?.message || "未知错误"));
        }
      })
      .finally(() => setCandidateLoading(false));
  };

  // Handle Create Job Submit
  const handleCreateJob = () => {
    if (!selectedBankId) {
      message.warning("请先选择题库");
      return;
    }
    if (selectedSenseKeys.length === 0) {
      message.warning("请至少选择一个词义版本");
      return;
    }

    const currentBank = banks.find((b) => b.id === selectedBankId);
    if (!currentBank) {
      message.error("未找到所选题库信息");
      return;
    }

    const selectedSenses = senses.filter((s) => selectedSenseKeys.includes(s.sense_version_id));
    const senseRefs = selectedSenses.map((s) => ({
      sense_id: s.sense_id,
      sense_version_id: s.sense_version_id,
    }));

    const idempotencyKey = `job-create-${Date.now()}-${Math.random().toString(36).substring(2, 9)}`;
    setCreatingJob(true);

    createGenerationJob(
      selectedBankId,
      {
        sense_refs: senseRefs,
        generation_config_version: "v1",
        quality_policy_version: "v1",
        expected_revision: currentBank.revision,
      },
      idempotencyKey
    )
      .then((resp) => {
        setCreatedJob({ job_id: resp.job_id, status: resp.status });
        message.success("生成任务登记成功 (202 Accepted)");
        loadJobs();
      })
      .catch((err) => {
        message.error("创建任务失败: " + err.message);
      })
      .finally(() => setCreatingJob(false));
  };

  const renderStatusTag = (status: string) => {
    switch (status) {
      case "published":
        return <Tag color="success" data-testid="badge-status-published">已发布 (Published)</Tag>;
      case "completed":
        return <Tag color="green" data-testid="badge-status-completed">已完成 (Completed)</Tag>;
      case "processing":
        return <Tag color="processing" data-testid="badge-status-processing">处理中 (Processing)</Tag>;
      case "pending":
        return <Tag color="gold" data-testid="badge-status-pending">排队中 (Pending)</Tag>;
      case "rejected":
        return <Tag color="error" data-testid="badge-status-rejected">已拒绝 (Rejected)</Tag>;
      case "failed":
        return <Tag color="error" data-testid="badge-status-failed">失败 (Failed)</Tag>;
      case "budget_exhausted":
        return <Tag color="magenta" data-testid="badge-status-budget_exhausted">预算耗尽 (Budget Exhausted)</Tag>;
      case "needs_attention":
        return <Tag color="warning" data-testid="badge-status-needs_attention">待人工关注 (Needs Attention)</Tag>;
      case "uncertain":
        return <Tag color="orange" data-testid="badge-status-uncertain">存疑 (Uncertain)</Tag>;
      case "pending_maintenance":
        return <Tag color="purple" data-testid="badge-status-pending_maintenance">待维护 (Pending Maintenance)</Tag>;
      default:
        return <Tag data-testid={`badge-status-${status}`}>{status}</Tag>;
    }
  };

  const renderCandidateDispositionTag = (disposition: string, candId: string) => {
    switch (disposition) {
      case "published":
        return <Tag color="success" data-testid={`candidate-disposition-${candId}`}>已发布 (Published)</Tag>;
      case "rejected":
        return <Tag color="error" data-testid={`candidate-disposition-${candId}`}>已拒绝 (Rejected)</Tag>;
      case "uncertain":
        return <Tag color="orange" data-testid={`candidate-disposition-${candId}`}>存疑 (Uncertain)</Tag>;
      case "pending_maintenance":
        return <Tag color="purple" data-testid={`candidate-disposition-${candId}`}>待维护 (Pending Maintenance)</Tag>;
      case "auto_passed":
        return <Tag color="cyan" data-testid={`candidate-disposition-${candId}`}>自动通过 (Auto Passed)</Tag>;
      default:
        return <Tag data-testid={`candidate-disposition-${candId}`}>{disposition}</Tag>;
    }
  };

  const formatTimestamp = (tsMicro: number) => {
    if (!tsMicro) return "-";
    const ms = tsMicro > 1e12 ? Math.floor(tsMicro / 1000) : tsMicro * 1000;
    return new Date(ms).toLocaleString("zh-CN");
  };

  return (
    <div style={{ padding: "24px", maxWidth: 1200, margin: "0 auto" }} data-testid="generation-view">
      <div style={{ marginBottom: 24, display: "flex", justifyContent: "space-between", alignItems: "center" }}>
        <div>
          <Title level={3} style={{ margin: 0 }}>
            <ThunderboltOutlined style={{ marginRight: 8, color: "#1677ff" }} />
            离线内容生成与质检 (W06)
          </Title>
          <Paragraph type="secondary" style={{ margin: "4px 0 0 0" }}>
            基于真实 Worker 与确定性 Provider 的题目生成、任务追踪、正负候选评测与预算核算
          </Paragraph>
        </div>
        <Button
          type="primary"
          icon={<PlusOutlined />}
          onClick={() => setActiveTab("create")}
          data-testid="create-job-btn"
        >
          新建生成任务
        </Button>
      </div>

      <Tabs
        activeKey={activeTab}
        onChange={(k) => setActiveTab(k)}
        items={[
          {
            key: "jobs",
            label: <span data-testid="tab-jobs">生成任务列表</span>,
            children: (
              <div>
                <Table
                  dataSource={jobs}
                  rowKey="id"
                  loading={jobsLoading}
                  pagination={false}
                  data-testid="jobs-table"
                  columns={[
                    {
                      title: "任务 ID",
                      dataIndex: "id",
                      key: "id",
                      render: (id, record) => (
                        <Space direction="vertical" size={2}>
                          <Text strong code>{id}</Text>
                          <Text type="secondary" style={{ fontSize: 12 }}>题库: {record.bank_name || record.bank_id}</Text>
                        </Space>
                      ),
                    },
                    {
                      title: "任务状态",
                      dataIndex: "status",
                      key: "status",
                      render: (status) => renderStatusTag(status),
                    },
                    {
                      title: "题目统计",
                      key: "stats",
                      render: (_, record) => (
                        <Space direction="vertical" size={2} style={{ fontSize: 13 }}>
                          <span>请求: <strong>{record.requested_count ?? record.requested}</strong></span>
                          <span>
                            发布: <Text type="success"><strong>{record.published_count ?? record.published}</strong></Text> |
                            拒绝: <Text type="danger"><strong>{record.rejected_count ?? record.rejected}</strong></Text> |
                            待处理: <strong>{record.pending_count ?? record.pending}</strong>
                          </span>
                        </Space>
                      ),
                    },
                    {
                      title: "预算占用",
                      key: "budget",
                      render: (_, record) => {
                        const b = record.budget_usage;
                        if (!b) return "-";
                        return (
                          <Space direction="vertical" size={2} style={{ fontSize: 12 }}>
                            <span>网络调用: {b.authorized_requests} / {b.max_requests}</span>
                            <span>Token: {b.total_tokens} / {b.max_tokens}</span>
                            {(b.budget_overrun || record.status === "budget_exhausted") && (
                              <Tag color="magenta" data-testid="alert-overrun" style={{ marginTop: 2 }}>
                                ⚠️ 预算超额预警
                              </Tag>
                            )}
                          </Space>
                        );
                      },
                    },
                    {
                      title: "创建时间",
                      dataIndex: "created_at",
                      key: "created_at",
                      render: (ts) => formatTimestamp(ts),
                    },
                    {
                      title: "操作",
                      key: "action",
                      render: (_, record) => (
                        <Button
                          type="link"
                          onClick={() => handleOpenJobDetail(record.id)}
                          data-testid={`view-job-btn-${record.id}`}
                        >
                          查看详情
                        </Button>
                      ),
                    },
                  ]}
                />

                <div style={{ display: "flex", justifyContent: "flex-end", marginTop: 16, gap: 8 }}>
                  <Button
                    disabled={jobsCursorStack.length === 0}
                    onClick={() => {
                      const newStack = [...jobsCursorStack];
                      const prevCursor = newStack.pop();
                      setJobsCursorStack(newStack);
                      setJobsCursor(prevCursor);
                    }}
                    data-testid="prev-page-btn"
                  >
                    上一页
                  </Button>
                  <Button
                    disabled={!jobsHasMore || !jobsNextCursor}
                    onClick={() => {
                      setJobsCursorStack([...jobsCursorStack, jobsCursor]);
                      setJobsCursor(jobsNextCursor || undefined);
                    }}
                    data-testid="next-page-btn"
                  >
                    下一页
                  </Button>
                </div>
              </div>
            ),
          },
          {
            key: "create",
            label: <span data-testid="tab-create">创建生成任务</span>,
            children: (
              <div style={{ maxWidth: 900 }}>
                {createdJob ? (
                  <Card style={{ textAlign: "center", padding: "32px 16px" }} data-testid="job-created-card">
                    <CheckCircleOutlined style={{ fontSize: 48, color: "#52c41a", marginBottom: 16 }} />
                    <Title level={4}>生成任务登记成功 (202 Accepted)</Title>
                    <Paragraph type="secondary">
                      任务已进入后台队列，系统已创建预算账本并在数据库中锁定输入指纹。
                    </Paragraph>
                    <div style={{ backgroundColor: "#fafafa", padding: 16, borderRadius: 6, margin: "16px auto", maxWidth: 450, textAlign: "left" }}>
                      <p><strong>Job ID:</strong> <Text code data-testid="created-job-id">{createdJob.job_id}</Text></p>
                      <p><strong>初始状态:</strong> <span data-testid="created-job-status">{renderStatusTag(createdJob.status)}</span></p>
                    </div>
                    <Space size="middle">
                      <Button
                        type="primary"
                        onClick={() => {
                          handleOpenJobDetail(createdJob.job_id);
                          setCreatedJob(null);
                        }}
                      >
                        查看任务详情与候选
                      </Button>
                      <Button onClick={() => setCreatedJob(null)}>
                        继续创建新任务
                      </Button>
                    </Space>
                  </Card>
                ) : (
                  <Card title="新建离线生成任务">
                    <div style={{ marginBottom: 20 }}>
                      <Text strong style={{ display: "block", marginBottom: 8 }}>
                        步骤 1: 选择目标题库
                      </Text>
                      <Select
                        placeholder="请选择要扩充题目的题库"
                        style={{ width: "100%" }}
                        value={selectedBankId}
                        onChange={(v) => setSelectedBankId(v)}
                        data-testid="bank-select"
                        options={banks.map((b) => ({
                          label: `${b.name} (版本 Rev.${b.revision})`,
                          value: b.id,
                        }))}
                      />
                    </div>

                    {selectedBankId && (
                      <div style={{ marginBottom: 20 }}>
                        <Text strong style={{ display: "block", marginBottom: 8 }}>
                          步骤 2: 发现并选择词义版本 (通过 W04 发现接口)
                        </Text>
                        <Table
                          dataSource={senses}
                          rowKey="sense_version_id"
                          loading={sensesLoading}
                          pagination={{ pageSize: 5 }}
                          size="small"
                          data-testid="sense-select-table"
                          rowSelection={{
                            type: "checkbox",
                            selectedRowKeys: selectedSenseKeys,
                            onChange: (keys) => setSelectedSenseKeys(keys),
                          }}
                          columns={[
                            {
                              title: "词汇 / 词形",
                              dataIndex: "canonical_form",
                              key: "canonical_form",
                              render: (text) => <Text strong>{text}</Text>,
                            },
                            {
                              title: "目标释义",
                              dataIndex: "definition",
                              key: "definition",
                            },
                            {
                              title: "词义版本 ID",
                              dataIndex: "sense_version_id",
                              key: "sense_version_id",
                              render: (id) => <Text code style={{ fontSize: 11 }}>{id}</Text>,
                            },
                            {
                              title: "状态",
                              dataIndex: "status",
                              key: "status",
                              render: (st) => <Tag color="blue">{st}</Tag>,
                            },
                          ]}
                        />
                      </div>
                    )}

                    {selectedSenseKeys.length > 0 && (
                      <Card
                        size="small"
                        title="步骤 3: 任务与预算账本确认"
                        style={{ marginBottom: 20, backgroundColor: "#f9f9f9" }}
                        data-testid="budget-preview"
                      >
                        <Descriptions column={2} size="small">
                          <Descriptions.Item label="已选词义数">
                            <Text strong>{selectedSenseKeys.length} 个词义</Text>
                          </Descriptions.Item>
                          <Descriptions.Item label="生成配置版本">
                            <Tag color="cyan">generation-v1</Tag>
                          </Descriptions.Item>
                          <Descriptions.Item label="质检策略版本">
                            <Tag color="purple">quality-v1</Tag>
                          </Descriptions.Item>
                          <Descriptions.Item label="单题调用预算上限">
                            <span>最多 3 次网络调用 / 5000 Token</span>
                          </Descriptions.Item>
                          <Descriptions.Item label="安全保证">
                            <Text type="secondary">确定性离线 Provider / 绝不泄露 API 凭证</Text>
                          </Descriptions.Item>
                        </Descriptions>
                      </Card>
                    )}

                    <Button
                      type="primary"
                      size="large"
                      onClick={handleCreateJob}
                      loading={creatingJob}
                      disabled={!selectedBankId || selectedSenseKeys.length === 0}
                      data-testid="submit-job-btn"
                      style={{ width: "100%" }}
                    >
                      确认提交任务 (202 Accepted 任务登记)
                    </Button>
                  </Card>
                )}
              </div>
            ),
          },
          {
            key: "findings",
            label: <span data-testid="tab-findings">质检异常发现</span>,
            children: (
              <div>
                <div style={{ marginBottom: 16, display: "flex", gap: 12, alignItems: "center" }}>
                  <span>状态过滤:</span>
                  <Select
                    value={findingStatusFilter}
                    onChange={(v) => {
                      setFindingStatusFilter(v);
                      setFindingsCursor(undefined);
                      setFindingsCursorStack([]);
                    }}
                    style={{ width: 160 }}
                    data-testid="findings-filter-status"
                    options={[
                      { label: "全部状态", value: "" },
                      { label: "待解决 (open)", value: "open" },
                      { label: "已解决 (resolved)", value: "resolved" },
                      { label: "已忽略 (ignored)", value: "ignored" },
                    ]}
                  />
                  <Button icon={<ReloadOutlined />} onClick={() => loadFindings(findingsCursor, findingStatusFilter)}>
                    刷新
                  </Button>
                </div>

                <Table
                  dataSource={findings}
                  rowKey="id"
                  loading={findingsLoading}
                  pagination={false}
                  data-testid="findings-table"
                  columns={[
                    {
                      title: "异常 ID",
                      dataIndex: "id",
                      key: "id",
                      render: (id) => <Text code>{id}</Text>,
                    },
                    {
                      title: "异常类型",
                      dataIndex: "finding_type",
                      key: "finding_type",
                      render: (ft) => <Tag color="orange">{ft}</Tag>,
                    },
                    {
                      title: "状态",
                      dataIndex: "status",
                      key: "status",
                      render: (st) => (st === "open" ? <Tag color="warning">待处置 (open)</Tag> : <Tag color="default">{st}</Tag>),
                    },
                    {
                      title: "关联任务 / 候选",
                      key: "refs",
                      render: (_, record) => (
                        <Space direction="vertical" size={2} style={{ fontSize: 11 }}>
                          <span>Job: <Text code>{record.job_id}</Text></span>
                          <span>Cand: <Text code>{record.candidate_id}</Text></span>
                        </Space>
                      ),
                    },
                    {
                      title: "检查器版本",
                      dataIndex: "checker_version",
                      key: "checker_version",
                      render: (v) => <Tag>{v}</Tag>,
                    },
                    {
                      title: "发现时间",
                      dataIndex: "created_at",
                      key: "created_at",
                      render: (ts) => formatTimestamp(ts),
                    },
                  ]}
                />

                <div style={{ display: "flex", justifyContent: "flex-end", marginTop: 16, gap: 8 }}>
                  <Button
                    disabled={findingsCursorStack.length === 0}
                    onClick={() => {
                      const newStack = [...findingsCursorStack];
                      const prevCursor = newStack.pop();
                      setFindingsCursorStack(newStack);
                      setFindingsCursor(prevCursor);
                    }}
                  >
                    上一页
                  </Button>
                  <Button
                    disabled={!findingsHasMore || !findingsNextCursor}
                    onClick={() => {
                      setFindingsCursorStack([...findingsCursorStack, findingsCursor]);
                      setFindingsCursor(findingsNextCursor || undefined);
                    }}
                    data-testid="findings-next-page-btn"
                  >
                    下一页
                  </Button>
                </div>
              </div>
            ),
          },
        ]}
      />

      {/* Job Detail Drawer */}
      <Drawer
        title={
          <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
            <span>任务详情:</span>
            <Text code>{selectedJobId}</Text>
          </div>
        }
        width={720}
        open={Boolean(selectedJobId)}
        onClose={() => setSelectedJobId(null)}
        keyboard={false}
        data-testid="job-detail-drawer"
      >
        {jobDetailLoading ? (
          <div style={{ textAlign: "center", padding: 40 }}><Spin size="large" /></div>
        ) : jobDetail ? (
          <div>
            <Descriptions column={2} bordered size="small" style={{ marginBottom: 20 }}>
              <Descriptions.Item label="题库">{jobDetail.bank_name || jobDetail.bank_id}</Descriptions.Item>
              <Descriptions.Item label="状态">{renderStatusTag(jobDetail.status)}</Descriptions.Item>
              <Descriptions.Item label="请求题目数">{jobDetail.requested_count ?? jobDetail.requested}</Descriptions.Item>
              <Descriptions.Item label="已正式发布">
                <Text type="success" strong>{jobDetail.published_count ?? jobDetail.published}</Text>
              </Descriptions.Item>
              <Descriptions.Item label="已拒绝候选">
                <Text type="danger">{jobDetail.rejected_count ?? jobDetail.rejected}</Text>
              </Descriptions.Item>
              <Descriptions.Item label="待处理项">{jobDetail.pending_count ?? jobDetail.pending}</Descriptions.Item>
              <Descriptions.Item label="网络调用授权">
                {jobDetail.budget_usage
                  ? `${jobDetail.budget_usage.authorized_requests} / ${jobDetail.budget_usage.max_requests}`
                  : "-"}
              </Descriptions.Item>
              <Descriptions.Item label="Token 占用">
                {jobDetail.budget_usage
                  ? `${jobDetail.budget_usage.total_tokens} / ${jobDetail.budget_usage.max_tokens}`
                  : "-"}
              </Descriptions.Item>
            </Descriptions>

            {(jobDetail.budget_usage?.budget_overrun || jobDetail.status === "budget_exhausted") && (
              <Alert
                message="预算已超限或已耗尽"
                description="该生成任务已达到持久账本授权上限（BUDGET_EXHAUSTED），系统已拒绝后续新增模型调用。"
                type="error"
                showIcon
                style={{ marginBottom: 20 }}
                data-testid="alert-overrun"
              />
            )}

            <Title level={5} style={{ marginTop: 20, marginBottom: 12 }}>
              生成题目候选列表 (Candidates)
            </Title>
            <Table
              dataSource={jobDetail.candidates || []}
              rowKey="id"
              size="small"
              pagination={false}
              data-testid="candidates-table"
              columns={[
                {
                  title: "候选 ID",
                  dataIndex: "id",
                  key: "id",
                  render: (id) => <Text code style={{ fontSize: 12 }}>{id}</Text>,
                },
                {
                  title: "轮次 / 序号",
                  key: "attempt",
                  render: (_, r) => <span>第 {r.attempt_number} 轮 · 候选 #{r.candidate_index}</span>,
                },
                {
                  title: "处置状态",
                  dataIndex: "disposition",
                  key: "disposition",
                  render: (disp, r) => renderCandidateDispositionTag(disp, r.id),
                },
                {
                  title: "正式版本 ID",
                  dataIndex: "published_version_id",
                  key: "published_version_id",
                  render: (pid) => pid ? <Text code style={{ color: "#52c41a", fontSize: 11 }}>{pid}</Text> : <Text type="secondary">-</Text>,
                },
                {
                  title: "操作",
                  key: "action",
                  render: (_, r) => (
                    <Button
                      type="link"
                      size="small"
                      icon={<FileSearchOutlined />}
                      onClick={() => handleInspectCandidate(r.id)}
                      data-testid={`inspect-candidate-btn-${r.id}`}
                    >
                      检视内容
                    </Button>
                  ),
                },
              ]}
            />
          </div>
        ) : null}
      </Drawer>

      {/* Candidate Detail Modal */}
      <Modal
        title={
          <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
            <span>题目候选内容检视:</span>
            <Text code>{inspectingCandidateId}</Text>
          </div>
        }
        open={Boolean(inspectingCandidateId)}
        onCancel={() => setInspectingCandidateId(null)}
        footer={null}
        width={680}
        destroyOnClose
      >
        <div data-testid="candidate-detail-modal">
          {candidateLoading ? (
            <div style={{ textAlign: "center", padding: 32 }}><Spin /></div>
          ) : candidateSessionLocked ? (
            <Alert
              message="活动会话防泄题保护锁定"
              description="当前正在进行作答会话。根据系统防作弊安全设计，做题期间禁止查看题目候选详情与正确答案。请在作答结束后查看。"
              type="warning"
              showIcon
              icon={<LockOutlined />}
              data-testid="session-locked-alert"
            />
          ) : candidateDetail ? (
            <div>
              <div style={{ marginBottom: 12 }}>
                <span style={{ marginRight: 8 }}>处置结果:</span>
                {renderCandidateDispositionTag(candidateDetail.disposition, inspectingCandidateId || "")}
              </div>

              <Card size="small" title="题干 (Stem)" style={{ marginBottom: 16 }} data-testid="candidate-stem">
                <Paragraph style={{ fontSize: 16, margin: 0, whiteSpace: "pre-wrap" }}>
                  {candidateDetail.candidate?.stem || "（无题干）"}
                </Paragraph>
              </Card>

              <Card size="small" title="候选选项 (Options)" style={{ marginBottom: 16 }} data-testid="candidate-options">
                {(candidateDetail.candidate?.options || []).map((opt, idx) => (
                  <div
                    key={idx}
                    style={{
                      display: "flex",
                      alignItems: "center",
                      justifyContent: "space-between",
                      padding: "8px 12px",
                      borderRadius: 4,
                      marginBottom: 6,
                      backgroundColor: opt.is_correct ? "#f6ffed" : "#fafafa",
                      border: opt.is_correct ? "1px solid #b7eb8f" : "1px solid #f0f0f0",
                    }}
                  >
                    <Text>{String.fromCharCode(65 + idx)}. {opt.text}</Text>
                    {opt.is_correct ? (
                      <Tag color="success" icon={<CheckCircleOutlined />}>正确答案</Tag>
                    ) : (
                      <Tag color="default">干扰项</Tag>
                    )}
                  </div>
                ))}
              </Card>

              {candidateDetail.candidate?.explanation && (
                <Card size="small" title="题目解析 (Explanation)" style={{ marginBottom: 16 }} data-testid="candidate-explanation">
                  <Paragraph type="secondary" style={{ margin: 0 }}>
                    {candidateDetail.candidate.explanation}
                  </Paragraph>
                </Card>
              )}

              <Card size="small" title="质检评测结果 (Checks)" data-testid="candidate-checks">
                <pre style={{ margin: 0, fontSize: 12, backgroundColor: "#f5f5f5", padding: 8, borderRadius: 4, overflowX: "auto" }}>
                  {JSON.stringify(candidateDetail.checks, null, 2)}
                </pre>
              </Card>
            </div>
          ) : null}
        </div>
      </Modal>
    </div>
  );
};
