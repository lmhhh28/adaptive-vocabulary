import React, { useState, useEffect, useCallback, useRef } from "react";
import {
  Typography,
  Tabs,
  Table,
  Button,
  Tag,
  Drawer,
  Modal,
  Form,
  Input,
  Select,
  Alert,
  Card,
  Descriptions,
  Space,
  Empty,
  Spin,
  message,
} from "antd";
import {
  SafetyCertificateOutlined,
  ExclamationCircleOutlined,
  BookOutlined,
  ToolOutlined,
  ReloadOutlined,
  StopOutlined,
  CheckCircleOutlined,
} from "@ant-design/icons";
import {
  listQualityFindings,
  resolveQualityFinding,
  listQuestionVersions,
  retireQuestionVersion,
  applyEvidenceDisposition,
  getLearningSummary,
  getLearningSenseDetail,
  getMaintenanceState,
  getActiveSession,
  ApiError,
} from "./api";
import {
  QualityFindingItem,
  QuestionVersionItem,
  LearningSummaryResponse,
  SenseLearningDetail,
  MaintenanceStatus,
} from "./types";

const { Title, Text, Paragraph } = Typography;
const { TextArea } = Input;

export interface QualityViewProps {
  onNavigate?: (path: string) => void;
}

export const QualityView: React.FC<QualityViewProps> = ({ onNavigate: _onNavigate }) => {
  const [activeTab, setActiveTab] = useState<string>("findings");

  // Global Context State
  const [activeSession, setActiveSession] = useState<any | null>(null);
  const [maintenance, setMaintenance] = useState<MaintenanceStatus | null>(null);

  // Tab 1: Findings State
  const [findings, setFindings] = useState<QualityFindingItem[]>([]);
  const [findingsLoading, setFindingsLoading] = useState<boolean>(false);
  const [selectedFinding, setSelectedFinding] = useState<QualityFindingItem | null>(null);
  const [findingDrawerOpen, setFindingDrawerOpen] = useState<boolean>(false);
  const [resolutionSuccess, setResolutionSuccess] = useState<boolean>(false);
  const [resolveLoading, setResolveLoading] = useState<boolean>(false);
  const [resolveForm] = Form.useForm();

  // Tab 2: Question Versions State
  const [questionVersions, setQuestionVersions] = useState<QuestionVersionItem[]>([]);
  const [qvLoading, setQvLoading] = useState<boolean>(false);
  const [selectedQv, setSelectedQv] = useState<QuestionVersionItem | null>(null);
  const [retireModalOpen, setRetireModalOpen] = useState<boolean>(false);
  const [retireReason, setRetireReason] = useState<string>("");
  const [retireLoading, setRetireLoading] = useState<boolean>(false);
  const [dispositionModalOpen, setDispositionModalOpen] = useState<boolean>(false);
  const [dispositionAction, setDispositionAction] = useState<"disputed" | "invalidate" | "restore_validity">("disputed");
  const [dispositionReason, setDispositionReason] = useState<string>("");
  const [dispositionLoading, setDispositionLoading] = useState<boolean>(false);
  const [dispositionResult, setDispositionResult] = useState<{ disposition_id: string; rebuild_run_id: string; status: string } | null>(null);

  // Tab 3: Learning Report State
  const [learningSummary, setLearningSummary] = useState<LearningSummaryResponse | null>(null);
  const [summaryLoading, setSummaryLoading] = useState<boolean>(false);
  const [senseIdInput, setSenseIdInput] = useState<string>("");
  const [senseDetail, setSenseDetail] = useState<SenseLearningDetail | null>(null);
  const [senseLoading, setSenseLoading] = useState<boolean>(false);
  const [senseSearched, setSenseSearched] = useState<boolean>(false);
  const [senseError, setSenseError] = useState<string | null>(null);

  // Tab 4: Maintenance State
  const [maintLoading, setMaintLoading] = useState<boolean>(false);
  const findingsRequestRef = useRef<number>(0);
  const questionVersionsRequestRef = useRef<number>(0);

  // Load Status Data (Active session & maintenance)
  const refreshStatus = useCallback(async () => {
    try {
      const sessResp = await getActiveSession();
      setActiveSession(sessResp.session);
    } catch {
      setActiveSession(null);
    }

    try {
      const maintResp = await getMaintenanceState();
      setMaintenance(maintResp);
    } catch {
      // Ignored
    }
  }, []);

  // Fetch Findings
  const loadFindings = useCallback(async () => {
    const requestId = ++findingsRequestRef.current;
    setFindingsLoading(true);
    try {
      let allItems: QualityFindingItem[] = [];
      let cursor: string | undefined = undefined;
      const seenCursors = new Set<string>();
      const seenIds = new Set<string>();
      while (findingsRequestRef.current === requestId) {
        const resp = await listQualityFindings({ limit: 100, cursor });
        const items = resp.items || [];
        for (const item of items) {
          if (!seenIds.has(item.id)) {
            seenIds.add(item.id);
            allItems.push(item);
          }
        }
        if (!resp.has_more || !resp.next_cursor || items.length === 0) {
          break;
        }
        if (seenCursors.has(resp.next_cursor)) {
          throw new Error("质量异常分页游标重复，已停止加载以避免循环");
        }
        seenCursors.add(resp.next_cursor);
        cursor = resp.next_cursor;
      }
      if (findingsRequestRef.current === requestId) {
        setFindings(allItems);
      }
    } catch (err: any) {
      if (findingsRequestRef.current === requestId) {
        message.error(err.message || "加载质量异常失败");
      }
    } finally {
      if (findingsRequestRef.current === requestId) {
        setFindingsLoading(false);
      }
    }
  }, []);

  // Fetch Question Versions
  const loadQuestionVersions = useCallback(async () => {
    const requestId = ++questionVersionsRequestRef.current;
    setQvLoading(true);
    try {
      let allItems: QuestionVersionItem[] = [];
      let cursor: string | undefined = undefined;
      const seenCursors = new Set<string>();
      const seenIds = new Set<string>();
      while (questionVersionsRequestRef.current === requestId) {
        const resp = await listQuestionVersions({ limit: 100, cursor });
        const items = resp.items || [];
        for (const item of items) {
          if (!seenIds.has(item.question_version_id)) {
            seenIds.add(item.question_version_id);
            allItems.push(item);
          }
        }
        if (!resp.has_more || !resp.next_cursor || items.length === 0) {
          break;
        }
        if (seenCursors.has(resp.next_cursor)) {
          throw new Error("题目版本分页游标重复，已停止加载以避免循环");
        }
        seenCursors.add(resp.next_cursor);
        cursor = resp.next_cursor;
      }
      if (questionVersionsRequestRef.current === requestId) {
        setQuestionVersions(allItems);
        await refreshStatus();
      }
    } catch (err: any) {
      if (questionVersionsRequestRef.current === requestId) {
        if (err instanceof ApiError && err.code === "SESSION_ACTIVE") {
          setActiveSession({ id: "active" });
        }
        message.error(err.message || "加载题目版本失败");
      }
    } finally {
      if (questionVersionsRequestRef.current === requestId) {
        setQvLoading(false);
      }
    }
  }, [refreshStatus]);

  // Fetch Learning Summary
  const loadLearningSummary = useCallback(async () => {
    setSummaryLoading(true);
    try {
      const resp = await getLearningSummary();
      setLearningSummary(resp);
    } catch (err: any) {
      message.error(err.message || "加载学习报告汇总失败");
    } finally {
      setSummaryLoading(false);
    }
  }, []);

  // Fetch Maintenance State
  const loadMaintenance = useCallback(async () => {
    setMaintLoading(true);
    try {
      const resp = await getMaintenanceState();
      setMaintenance(resp);
    } catch (err: any) {
      message.error(err.message || "加载维护状态失败");
    } finally {
      setMaintLoading(false);
    }
  }, []);

  useEffect(() => {
    refreshStatus();
    loadFindings();
    loadQuestionVersions();
    loadLearningSummary();
  }, [refreshStatus, loadFindings, loadQuestionVersions, loadLearningSummary]);

  // Handle Finding Resolution Submission
  const handleSubmitResolution = async () => {
    if (!selectedFinding) return;
    let values: any;
    try {
      values = await resolveForm.validateFields();
    } catch {
      // Validation failed, do not proceed with submission
      return;
    }
    const action = values.action || "exclude";
    const reason = values.reason || "人工质检审核判定不合格，予以排除";
    try {
      setResolveLoading(true);
      const resp = await resolveQualityFinding(selectedFinding.id, {
        action,
        reason,
        expected_revision: selectedFinding.revision,
      });

      message.success("处置记录已保存");
      setResolutionSuccess(true);
      // Update local findings
      setFindings((prev) =>
        prev.map((f) =>
          f.id === selectedFinding.id
            ? { ...f, status: resp.status, resolution_id: resp.resolution_id, revision: resp.revision }
            : f
        )
      );
      setSelectedFinding((prev) =>
        prev ? { ...prev, status: resp.status, resolution_id: resp.resolution_id, revision: resp.revision } : null
      );
    } catch (err: any) {
      if (err instanceof ApiError) {
        if (err.code === "SESSION_ACTIVE") {
          message.error("存在活动作答会话，无法提交处置记录");
          setActiveSession({ id: "active" });
        } else if (err.code === "MAINTENANCE") {
          message.error("系统处于维护模式，禁止写入");
        } else {
          message.error(`处置保存失败: ${err.message}`);
        }
      } else {
        message.error(`处置保存失败: ${err?.message || "未知错误"}`);
      }
    } finally {
      setResolveLoading(false);
    }
  };

  // Handle Question Version Retire
  const handleConfirmRetire = async () => {
    if (!selectedQv) return;
    if (!retireReason.trim()) {
      message.warning("请填写退役原因");
      return;
    }
    setRetireLoading(true);
    try {
      const resp = await retireQuestionVersion(selectedQv.question_version_id, {
        reason: retireReason,
        expected_revision: selectedQv.revision,
      });
      message.success(`题目版本 ${resp.version_id} 已退役`);
      setQuestionVersions((prev) =>
        prev.map((q) =>
          q.question_version_id === resp.version_id ? { ...q, status: "retired", revision: resp.revision } : q
        )
      );
      setRetireModalOpen(false);
      setRetireReason("");
    } catch (err: any) {
      if (err instanceof ApiError) {
        if (err.code === "SESSION_ACTIVE") {
          message.error("存在活动会话，无法执行退役操作");
          setActiveSession({ id: "active" });
        } else if (err.code === "MAINTENANCE") {
          message.error("系统处于维护重建模式，禁止修改题目版本");
        } else {
          message.error(`退役失败: ${err.message}`);
        }
      } else {
        message.error(`退役失败: ${err?.message || "未知错误"}`);
      }
    } finally {
      setRetireLoading(false);
    }
  };

  // Handle Evidence Disposition
  const handleConfirmDisposition = async () => {
    if (!selectedQv) return;
    if (!dispositionReason.trim()) {
      message.warning("请填写处置原因");
      return;
    }
    setDispositionLoading(true);
    try {
      const resp = await applyEvidenceDisposition(selectedQv.question_version_id, {
        action: dispositionAction,
        reason: dispositionReason,
        expected_revision: selectedQv.revision,
      });
      setDispositionResult(resp);
      message.success(`证据处置已生效，重建状态: ${resp.status}`);
      // Refresh question versions
      loadQuestionVersions();
    } catch (err: any) {
      if (err instanceof ApiError) {
        if (err.code === "SESSION_ACTIVE") {
          message.error("存在活动会话，无法执行证据处置");
          setActiveSession({ id: "active" });
        } else if (err.code === "MAINTENANCE") {
          message.error("系统处于维护模式，禁止提交证据处置");
        } else {
          message.error(`证据处置失败: ${err.message}`);
        }
      } else {
        message.error(`证据处置失败: ${err?.message || "未知错误"}`);
      }
    } finally {
      setDispositionLoading(false);
    }
  };

  // Handle Query Sense Detail
  const handleQuerySense = async () => {
    const sid = senseIdInput.trim();
    if (!sid) {
      message.warning("请输入有效词义 ID");
      return;
    }
    setSenseLoading(true);
    setSenseSearched(true);
    setSenseError(null);
    setSenseDetail(null);
    try {
      const resp = await getLearningSenseDetail(sid);
      setSenseDetail(resp);
    } catch (err: any) {
      if (err instanceof ApiError && err.code === "SESSION_ACTIVE") {
        setSenseError("存在活动作答会话，学习详情已锁定");
        setActiveSession({ id: "active" });
      } else {
        setSenseError(err?.message || "查询词义学习详情失败");
      }
    } finally {
      setSenseLoading(false);
    }
  };

  return (
    <div style={{ padding: "24px 0" }} data-testid="quality-view">
      {/* Title & Navigation */}
      <div style={{ marginBottom: 20 }}>
        <Title level={3} style={{ marginBottom: 4 }}>
          质量处置、学习报告与维护
        </Title>
        <Paragraph type="secondary">
          管理内容生成异常、题目版本退役与证据处置，查看学习掌握投影及底层系统维护状态。
        </Paragraph>
      </div>

      {/* Global Alerts */}
      {activeSession && (
        <Alert
          type="warning"
          message="存在活动作答会话"
          description="当前有正在进行的作答会话（练习或考试），质量处置、题目退役与敏感学习详情操作已受保护锁定。请先完成或结束当前会话。"
          showIcon
          icon={<ExclamationCircleOutlined />}
          style={{ marginBottom: 16 }}
          data-testid="active-session-alert"
        />
      )}

      {maintenance?.mode === "rebuilding" && (
        <Alert
          type="warning"
          message="系统处于维护重建模式"
          description={`当前投影正在重建中 (版本: ${maintenance.active_version || "未知"})，系统处于维护禁写状态，处置与退役提交已被暂停。`}
          showIcon
          icon={<ToolOutlined />}
          style={{ marginBottom: 16 }}
          data-testid="maintenance-alert"
        />
      )}

      {maintenance?.run_status === "failed" && (
        <Alert
          type="error"
          message="维护/重建业务失败"
          description={`投影重建发生业务失败 (run_status: failed, 建议操作: ${maintenance.next_action || "resume_or_abort"})。请参考系统运行手册执行维护排查。`}
          showIcon
          icon={<StopOutlined />}
          style={{ marginBottom: 16 }}
          data-testid="maintenance-failed-alert"
        />
      )}

      {/* Main Tabs */}
      <Tabs
        activeKey={activeTab}
        onChange={(k) => {
          setActiveTab(k);
          if (k === "maintenance") loadMaintenance();
        }}
        items={[
          {
            key: "findings",
            label: (
              <span data-testid="tab-findings">
                <SafetyCertificateOutlined /> 质量异常
              </span>
            ),
            children: (
              <div>
                <div style={{ display: "flex", justifyContent: "space-between", marginBottom: 16 }}>
                  <Text strong>内容生成质检异常列表 ({findings.length})</Text>
                  <Button icon={<ReloadOutlined />} onClick={loadFindings} loading={findingsLoading}>
                    刷新异常
                  </Button>
                </div>

                <Table
                  dataSource={findings}
                  rowKey="id"
                  loading={findingsLoading}
                  data-testid="findings-table"
                  pagination={{ pageSize: 10 }}
                  scroll={{ x: 900 }}
                  columns={[
                    {
                      title: "异常 ID",
                      dataIndex: "id",
                      key: "id",
                      render: (id: string) => (
                        <Text code style={{ fontSize: 12 }} data-testid={`finding-id-${id}`}>
                          {id.slice(0, 8)}...
                        </Text>
                      ),
                    },
                    {
                      title: "任务 ID",
                      dataIndex: "job_id",
                      key: "job_id",
                      render: (jid: string) => <Text code style={{ fontSize: 12 }}>{jid ? `${jid.slice(0, 8)}...` : "-"}</Text>,
                    },
                    {
                      title: "候选 ID",
                      dataIndex: "candidate_id",
                      key: "candidate_id",
                      render: (cid: string) => <Text code style={{ fontSize: 12 }}>{cid ? `${cid.slice(0, 8)}...` : "-"}</Text>,
                    },
                    {
                      title: "异常类型",
                      dataIndex: "finding_type",
                      key: "finding_type",
                      render: (ft: string) => <Tag color={ft === "uncertain" ? "orange" : "blue"}>{ft}</Tag>,
                    },
                    {
                      title: "状态",
                      dataIndex: "status",
                      key: "status",
                      render: (st: string) => (
                        <Tag color={st === "resolved" ? "green" : "volcano"}>
                          {st === "resolved" ? "已记录处置" : "待处置 (open)"}
                        </Tag>
                      ),
                    },
                    {
                      title: "检查版本",
                      dataIndex: "checker_version",
                      key: "checker_version",
                      render: (cv: string) => <Text type="secondary">{cv}</Text>,
                    },
                    {
                      title: "创建时间",
                      dataIndex: "created_at",
                      key: "created_at",
                      render: (ts: number) => new Date(Math.floor(ts / 1000)).toLocaleString(),
                    },
                    {
                      title: "操作",
                      key: "action",
                      render: (_, record: QualityFindingItem) => (
                        <Button
                          size="small"
                          type="link"
                          data-testid="resolve-finding-btn"
                          onClick={() => {
                            setSelectedFinding(record);
                            setResolutionSuccess(record.status === "resolved");
                            resolveForm.resetFields();
                            resolveForm.setFieldsValue({
                              action: "exclude",
                              reason: "人工质检审核判定不合格，予以排除",
                            });
                            setFindingDrawerOpen(true);
                          }}
                        >
                          {record.status === "resolved" ? "查看详情" : "处置记录"}
                        </Button>
                      ),
                    },
                  ]}
                />
              </div>
            ),
          },
          {
            key: "question_versions",
            label: (
              <span data-testid="tab-question-versions">
                <ToolOutlined /> 题目版本与处置
              </span>
            ),
            children: (
              <div>
                <div style={{ display: "flex", justifyContent: "space-between", marginBottom: 16 }}>
                  <Text strong>题目版本列表 ({questionVersions.length})</Text>
                  <Button icon={<ReloadOutlined />} onClick={loadQuestionVersions} loading={qvLoading}>
                    刷新题目版本
                  </Button>
                </div>

                <Table
                  dataSource={questionVersions}
                  rowKey="question_version_id"
                  loading={qvLoading}
                  data-testid="question-versions-table"
                  pagination={{ pageSize: 10 }}
                  scroll={{ x: 760 }}
                  columns={[
                    {
                      title: "版本 ID",
                      dataIndex: "question_version_id",
                      key: "question_version_id",
                      render: (id: string) => (
                        <Text code style={{ fontSize: 12 }} data-testid={`qv-id-${id}`}>
                          {id.slice(0, 8)}...
                        </Text>
                      ),
                    },
                    {
                      title: "词义 ID",
                      dataIndex: "sense_id",
                      key: "sense_id",
                      render: (sid: string) => (
                        <Text code style={{ fontSize: 12 }}>
                          {sid.slice(0, 8)}...
                        </Text>
                      ),
                    },
                    {
                      title: "题型",
                      dataIndex: "task_family",
                      key: "task_family",
                      render: (tf: string) => <Tag color="geekblue">{tf}</Tag>,
                    },
                    {
                      title: "状态",
                      dataIndex: "status",
                      key: "status",
                      render: (st: string) => {
                        let color = "blue";
                        if (st === "retired") color = "default";
                        else if (st === "invalidated") color = "red";
                        else if (st === "disputed") color = "orange";
                        else if (st === "published") color = "green";
                        return (
                          <Tag color={color} data-testid={`qv-status-${st}`}>
                            {st === "retired" ? (
                              <span data-testid="status-retired">retired (已退役)</span>
                            ) : (
                              st
                            )}
                          </Tag>
                        );
                      },
                    },
                    {
                      title: "操作",
                      key: "actions",
                      render: (_, record: QuestionVersionItem) => (
                        <Space>
                          <Button
                            size="small"
                            danger
                            disabled={record.status === "retired" || activeSession !== null || maintenance?.mode === "rebuilding"}
                            data-testid="retire-btn"
                            onClick={() => {
                              setSelectedQv(record);
                              setRetireReason("淘汰旧题型并停用");
                              setRetireModalOpen(true);
                            }}
                          >
                            退役
                          </Button>
                          <Button
                            size="small"
                            disabled={activeSession !== null || maintenance?.mode === "rebuilding"}
                            data-testid="disposition-btn"
                            onClick={() => {
                              setSelectedQv(record);
                              setDispositionAction("disputed");
                              setDispositionReason("人工复核标记证据争议");
                              setDispositionResult(null);
                              setDispositionModalOpen(true);
                            }}
                          >
                            证据处置
                          </Button>
                        </Space>
                      ),
                    },
                  ]}
                />
              </div>
            ),
          },
          {
            key: "learning",
            label: (
              <span data-testid="tab-learning-report">
                <BookOutlined /> 学习报告
              </span>
            ),
            children: (
              <div>
                <div style={{ marginBottom: 20 }}>
                  <Text strong>学习掌握投影汇总</Text>
                  <Button
                    icon={<ReloadOutlined />}
                    size="small"
                    style={{ marginLeft: 12 }}
                    onClick={loadLearningSummary}
                    loading={summaryLoading}
                  >
                    刷新汇总
                  </Button>
                </div>

                <div
                  style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(220px, 1fr))", gap: 16, marginBottom: 24 }}
                  data-testid="learning-summary"
                >
                  <Card size="small" title="活动投影版本">
                    <Text strong style={{ fontSize: 18, color: "#1677ff" }}>
                      {learningSummary?.projection_version || "暂无投影"}
                    </Text>
                  </Card>
                  <Card size="small" title="观测证据策略">
                    <Text strong style={{ fontSize: 16 }}>
                      {learningSummary?.evidence_conditions?.ordinary_evidence_policy || "observation-v2"}
                    </Text>
                  </Card>
                  <Card size="small" title="系统运行模式">
                    <Tag color={learningSummary?.evidence_conditions?.mode === "active" ? "green" : "orange"}>
                      {learningSummary?.evidence_conditions?.mode || "active"}
                    </Tag>
                  </Card>
                  <Card size="small" title="各题型学习条目计数">
                    {learningSummary?.counts_by_task && Object.keys(learningSummary.counts_by_task).length > 0 ? (
                      <div>
                        {Object.entries(learningSummary.counts_by_task).map(([tf, cnt]) => (
                          <Tag key={tf} color="blue" style={{ marginBottom: 4 }}>
                            {tf}: {cnt}
                          </Tag>
                        ))}
                      </div>
                    ) : (
                      <Text type="secondary" style={{ fontSize: 13 }}>暂无条目</Text>
                    )}
                  </Card>
                </div>

                {/* Sense query input */}
                <Card size="small" title="查询特定词义掌握详情" data-testid="sense-query-section">
                  <Space style={{ marginBottom: 16 }}>
                    <Input
                      placeholder="输入词义 Sense ID..."
                      value={senseIdInput}
                      onChange={(e) => setSenseIdInput(e.target.value)}
                      style={{ width: 320 }}
                      data-testid="sense-input"
                    />
                    <Button type="primary" onClick={handleQuerySense} loading={senseLoading} data-testid="query-sense-btn">
                      查询词义学习详情
                    </Button>
                  </Space>

                  {senseLoading && <Spin style={{ display: "block", margin: "20px 0" }} />}

                  {senseError && (
                    <Alert
                      type="error"
                      message="查询失败"
                      description={senseError}
                      showIcon
                      style={{ marginTop: 12 }}
                      data-testid="sense-error-alert"
                    />
                  )}

                  {!senseLoading && !senseError && senseSearched && (!senseDetail || !senseDetail.tasks || senseDetail.tasks.length === 0 || senseDetail.tasks.every(t => (t.evidence?.ordinary_samples || 0) === 0)) && (
                    <div style={{ padding: "20px 0" }}>
                      <Empty description="暂无足够学习样本" data-testid="learning-empty-state" />
                      <Alert
                        message="证据不足"
                        description="当前词义在有效投影中暂无足够作答样本，统计指标为空，不使用 0% 代替实际样本缺失。"
                        type="info"
                        showIcon
                        style={{ marginTop: 16 }}
                        data-testid="no-samples-alert"
                      />
                    </div>
                  )}

                  {!senseLoading && senseDetail && (senseDetail.tasks || []).some(t => (t.evidence?.ordinary_samples || 0) > 0) && (
                    <div style={{ marginTop: 16 }} data-testid="sense-learning-detail">
                      <Descriptions bordered size="small" column={2} style={{ marginBottom: 16 }}>
                        <Descriptions.Item label="词义 ID">{senseDetail.sense_id}</Descriptions.Item>
                        <Descriptions.Item label="投影版本">{senseDetail.projection_version}</Descriptions.Item>
                      </Descriptions>

                      {(senseDetail.tasks || []).map((task) => (
                        <Card
                          key={task.task_family}
                          size="small"
                          title={`题型: ${task.task_family}`}
                          style={{ marginBottom: 12 }}
                        >
                          <Descriptions size="small" column={3}>
                            <Descriptions.Item label="观测样本数">
                              <Text strong data-testid="ordinary-samples-count">
                                {task.evidence?.ordinary_samples ?? "null (暂无)"}
                              </Text>
                            </Descriptions.Item>
                            <Descriptions.Item label="正确数">
                              <Text strong data-testid="ordinary-correct-count">
                                {task.evidence?.ordinary_correct ?? "null"}
                              </Text>
                            </Descriptions.Item>
                            <Descriptions.Item label="需要巩固">
                              {task.evidence?.needs_consolidation ? <Tag color="volcano">是</Tag> : <Tag color="green">否</Tag>}
                            </Descriptions.Item>
                            <Descriptions.Item label="FSRS 状态">
                              <Tag color="cyan">{task.memory?.fsrs_state || "未评估"}</Tag>
                            </Descriptions.Item>
                            <Descriptions.Item label="记忆稳定性 (Stability)">
                              <Text data-testid="fsrs-stability">{task.memory?.stability != null ? task.memory.stability.toFixed(2) : "null"}</Text>
                            </Descriptions.Item>
                            <Descriptions.Item label="难度 (Difficulty)">
                              <Text data-testid="fsrs-difficulty">{task.memory?.difficulty != null ? task.memory.difficulty.toFixed(2) : "null"}</Text>
                            </Descriptions.Item>
                          </Descriptions>
                        </Card>
                      ))}
                    </div>
                  )}
                </Card>
              </div>
            ),
          },
          {
            key: "maintenance",
            label: (
              <span data-testid="tab-maintenance">
                <ToolOutlined /> 系统维护状态
              </span>
            ),
            children: (
              <div>
                <div style={{ display: "flex", justifyContent: "space-between", marginBottom: 16 }}>
                  <Text strong>投影维护与对账运行控制</Text>
                  <Button icon={<ReloadOutlined />} onClick={loadMaintenance} loading={maintLoading}>
                    刷新维护状态
                  </Button>
                </div>

                <Card size="small" data-testid="maintenance-info" style={{ marginBottom: 16 }}>
                  <Descriptions bordered size="small" column={2}>
                    <Descriptions.Item label="运行模式 (Mode)">
                      <Tag color={maintenance?.mode === "rebuilding" ? "volcano" : "green"} data-testid="maint-mode">
                        {maintenance?.mode || "active"}
                      </Tag>
                    </Descriptions.Item>
                    <Descriptions.Item label="当前活动版本 (Active Version)">
                      <Text strong data-testid="maint-active-version">{maintenance?.active_version || "未知"}</Text>
                    </Descriptions.Item>
                    <Descriptions.Item label="重建运行 ID (Run ID)">
                      <Text code data-testid="maint-run-id">{maintenance?.run_id || "无"}</Text>
                    </Descriptions.Item>
                    <Descriptions.Item label="运行状态 (Run Status)">
                      <Tag
                        color={
                          maintenance?.run_status === "failed"
                            ? "red"
                            : maintenance?.run_status === "running"
                            ? "orange"
                            : "green"
                        }
                        data-testid="maint-run-status"
                      >
                        {maintenance?.run_status || "空闲 (idle)"}
                      </Tag>
                    </Descriptions.Item>
                    <Descriptions.Item label="需要重建 (Requires Rebuild)">
                      <Tag color={maintenance?.requires_rebuild ? "warning" : "default"} data-testid="maint-requires-rebuild">
                        {maintenance?.requires_rebuild ? "是" : "否"}
                      </Tag>
                    </Descriptions.Item>
                    <Descriptions.Item label="建议下一步动作 (Next Action)">
                      <Text strong data-testid="maint-next-action">{maintenance?.next_action || "无"}</Text>
                    </Descriptions.Item>
                  </Descriptions>
                </Card>

                {maintenance?.run_status === "failed" && (
                  <Alert
                    type="error"
                    message="业务失败: 投影重建未成功 (failed)"
                    description={`虽然维护状态接口响应正常 (HTTP 200)，但内部重建执行状态为 failed。推荐后续操作: ${maintenance.next_action || "resume_or_abort"}。`}
                    showIcon
                    icon={<StopOutlined />}
                    data-testid="maintenance-status-failed"
                    style={{ marginBottom: 16 }}
                  />
                )}

                {maintenance?.run_status !== "failed" && maintenance?.mode === "active" && (
                  <Alert
                    type="success"
                    message="系统维护状态正常"
                    description="投影与模型版本一致，无挂起的维护或失败任务。"
                    showIcon
                    icon={<CheckCircleOutlined />}
                    data-testid="maintenance-status-ok"
                    style={{ marginBottom: 16 }}
                  />
                )}

                <Paragraph type="secondary">
                  提示：依据系统设计，重建恢复与数据库维护操作使用官方 CLI 与运行手册执行，界面不提供未实现依据的重建或备份按钮。
                </Paragraph>
              </div>
            ),
          },
        ]}
      />

      {/* Finding Detail & Resolution Drawer */}
      <Drawer
        title="质量异常详情与处置"
        width="min(600px, 100vw)"
        open={findingDrawerOpen}
        onClose={() => setFindingDrawerOpen(false)}
        data-testid="finding-detail-drawer"
      >
        {selectedFinding && (
          <div>
            <Descriptions bordered size="small" column={1} style={{ marginBottom: 16 }}>
              <Descriptions.Item label="异常 ID">{selectedFinding.id}</Descriptions.Item>
              <Descriptions.Item label="任务 ID">{selectedFinding.job_id || "无"}</Descriptions.Item>
              <Descriptions.Item label="候选 ID">{selectedFinding.candidate_id || "无"}</Descriptions.Item>
              <Descriptions.Item label="异常类型">
                <Tag color="orange">{selectedFinding.finding_type}</Tag>
              </Descriptions.Item>
              <Descriptions.Item label="状态">
                <Tag color={selectedFinding.status === "resolved" ? "green" : "volcano"}>
                  {selectedFinding.status === "resolved" ? "已记录处置" : "待处置 (open)"}
                </Tag>
              </Descriptions.Item>
              <Descriptions.Item label="检查器版本">{selectedFinding.checker_version}</Descriptions.Item>
            </Descriptions>

            <div style={{ marginBottom: 16 }}>
              <Text strong>详细质检证据 (details):</Text>
              <pre
                style={{
                  backgroundColor: "#f5f5f5",
                  padding: 12,
                  borderRadius: 4,
                  marginTop: 6,
                  maxHeight: 200,
                  overflow: "auto",
                  fontSize: 12,
                }}
                data-testid="finding-details-json"
              >
                {typeof selectedFinding.details === "object"
                  ? JSON.stringify(selectedFinding.details, null, 2)
                  : (selectedFinding.details || selectedFinding.details_json || "无")}
              </pre>
            </div>

            <Alert
              message="处置记录说明"
              description="此操作仅记录处置结果（已记录处置），不会修改题目内容或假称内容已发布。"
              type="info"
              showIcon
              style={{ marginBottom: 16 }}
              data-testid="resolution-notice"
            />

            {selectedFinding.status === "resolved" || resolutionSuccess ? (
              <Card style={{ backgroundColor: "#f6ffed", borderColor: "#b7eb8f" }} data-testid="resolution-success-card">
                <Space direction="vertical">
                  <Text strong style={{ color: "#389e0d" }}>
                    <CheckCircleOutlined /> 已记录处置
                  </Text>
                  <Text type="secondary" style={{ fontSize: 13 }}>
                    该异常已保存正式处置记录。系统未对题目版本内容进行伪造发布。
                  </Text>
                  {selectedFinding.resolution_id && (
                    <Text code style={{ fontSize: 12 }}>
                      Resolution ID: {selectedFinding.resolution_id}
                    </Text>
                  )}
                </Space>
              </Card>
            ) : (
              <Form
                form={resolveForm}
                layout="vertical"
                initialValues={{ action: "exclude", reason: "人工质检审核判定不合格，予以排除" }}
              >
                <Form.Item
                  name="action"
                  label="处置动作"
                  rules={[{ required: true, message: "请选择处置动作" }]}
                >
                  <Select data-testid="resolution-action-select">
                    <Select.Option value="exclude">排除候选 (exclude)</Select.Option>
                    <Select.Option value="revise">记录修订意图 (revise)</Select.Option>
                    <Select.Option value="regenerate">标记重新生成 (regenerate)</Select.Option>
                    <Select.Option value="restore_validity">恢复有效 (restore_validity)</Select.Option>
                  </Select>
                </Form.Item>

                <Form.Item
                  name="reason"
                  label="处置原因"
                  rules={[{ required: true, message: "请填写处置原因" }]}
                >
                  <TextArea
                    rows={3}
                    placeholder="填写详细的人工审核/处置理由..."
                    data-testid="resolution-reason-input"
                  />
                </Form.Item>

                <Button
                  type="primary"
                  block
                  onClick={handleSubmitResolution}
                  loading={resolveLoading}
                  disabled={activeSession !== null || maintenance?.mode === "rebuilding"}
                  data-testid="submit-resolution-btn"
                >
                  保存处置记录
                </Button>
              </Form>
            )}
          </div>
        )}
      </Drawer>

      {/* Retire Modal */}
      <Modal
        title="确认退役题目版本"
        open={retireModalOpen}
        onCancel={() => setRetireModalOpen(false)}
        onOk={handleConfirmRetire}
        confirmLoading={retireLoading}
        okText="确认退役"
        cancelText="取消"
        okButtonProps={{ danger: true, "data-testid": "submit-retire-btn" as any }}
        data-testid="retire-modal"
      >
        <div data-testid="retire-modal-content">
          <Paragraph>
            退役题目版本将阻止后续自适应练习选题选取该题目版本。
            <Text strong> 历史作答事实、暴露记录与作答评估将完整保留，不被删除或篡改。</Text>
          </Paragraph>
          <Form layout="vertical">
            <Form.Item label="退役原因" required>
              <TextArea
                rows={3}
                value={retireReason}
                onChange={(e) => setRetireReason(e.target.value)}
                placeholder="请输入退役原因（如题目缺陷、淘汰旧版题型等）"
                data-testid="retire-reason-input"
              />
            </Form.Item>
          </Form>
        </div>
      </Modal>

      {/* Evidence Disposition Modal */}
      <Modal
        title="证据处置 (Evidence Disposition)"
        open={dispositionModalOpen}
        onCancel={() => setDispositionModalOpen(false)}
        onOk={handleConfirmDisposition}
        confirmLoading={dispositionLoading}
        okText="提交证据处置"
        cancelText="取消"
        okButtonProps={{ "data-testid": "submit-disposition-btn" as any }}
        data-testid="disposition-modal"
      >
        <div data-testid="disposition-modal-content">
          <Paragraph type="secondary">
            处置操作将标记该题目版本所对应的历史学习证据有效性，并同步触发模型投影重建。
          </Paragraph>

          {dispositionResult && (
            <Alert
              type="success"
              message="证据处置已完成"
              description={`处置 ID: ${dispositionResult.disposition_id} | 重建运行状态: ${dispositionResult.status}`}
              showIcon
              style={{ marginBottom: 16 }}
              data-testid="disposition-result"
            />
          )}

          <Form layout="vertical">
            <Form.Item label="处置动作" required>
              <Select
                value={dispositionAction}
                onChange={(v) => setDispositionAction(v)}
                data-testid="disposition-action-select"
              >
                <Select.Option value="disputed">争议标记 (disputed)</Select.Option>
                <Select.Option value="invalidate">作废证据 (invalidate)</Select.Option>
                <Select.Option value="restore_validity">恢复有效 (restore_validity)</Select.Option>
              </Select>
            </Form.Item>
            <Form.Item label="处置原因" required>
              <TextArea
                rows={3}
                value={dispositionReason}
                onChange={(e) => setDispositionReason(e.target.value)}
                placeholder="请输入处置原因..."
                data-testid="disposition-reason-input"
              />
            </Form.Item>
          </Form>
        </div>
      </Modal>
    </div>
  );
};
