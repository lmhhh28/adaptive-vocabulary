import React, { useEffect, useState } from "react";
import { Row, Col, Card, Typography, Button, Space, Tag, Spin } from "antd";
import { ReadOutlined, FormOutlined, DatabaseOutlined, AppstoreOutlined } from "@ant-design/icons";
import { request } from "../api/transport";

const { Title, Paragraph, Text } = Typography;

export interface HomeViewProps {
  onNavigate: (path: string) => void;
}

export const HomeView: React.FC<HomeViewProps> = ({ onNavigate }) => {
  const [loading, setLoading] = useState(true);
  const [health, setHealth] = useState<any>(null);

  useEffect(() => {
    request("/api/v1/health")
      .then((data) => setHealth(data))
      .catch(() => {})
      .finally(() => setLoading(false));
  }, []);

  return (
    <div style={{ maxWidth: 1000, margin: "0 auto", padding: "24px 0" }} data-testid="home-view">
      <div style={{ marginBottom: 32 }}>
        <Title level={2} style={{ marginBottom: 8 }}>
          Adaptive Vocabulary
        </Title>
        <Paragraph type="secondary" style={{ fontSize: 16 }}>
          基于 FSRS 记忆算法与语境辨析的自适应词汇学习与阶段评测系统
        </Paragraph>
        {loading ? (
          <Spin size="small" />
        ) : (
          health && (
            <Space>
              <Tag color="green">服务正常</Tag>
              <Text type="secondary">合约版本: {health.contract_version || "1.1.0"}</Text>
              <Text type="secondary">运行模式: {health.mode || "active"}</Text>
            </Space>
          )
        )}
      </div>

      <Row gutter={[24, 24]}>
        <Col xs={24} sm={12}>
          <Card
            hoverable
            title={<Space><ReadOutlined style={{ color: "#1677ff" }} />自适应练习</Space>}
            extra={<Button type="primary" onClick={() => onNavigate("/practice")}>进入练习</Button>}
            data-testid="card-practice"
          >
            <Paragraph type="secondary">
              点击选项立即校验，答对自动下一题，答错原位停留解析。支持题卡导航与错题穿插纠错。
            </Paragraph>
          </Card>
        </Col>

        <Col xs={24} sm={12}>
          <Card
            hoverable
            title={<Space><FormOutlined style={{ color: "#52c41a" }} />阶段考试</Space>}
            extra={<Button onClick={() => onNavigate("/exams/new")}>创建考试</Button>}
            data-testid="card-exam"
          >
            <Paragraph type="secondary">
              阶段全真模拟测评，固定题卡自由改答，交卷前严格防泄题，统一交卷并输出多维诊断报告。
            </Paragraph>
          </Card>
        </Col>

        <Col xs={24} sm={12}>
          <Card
            hoverable
            title={<Space><DatabaseOutlined style={{ color: "#fa8c16" }} />题库与内容管理</Space>}
            extra={<Button onClick={() => onNavigate("/banks")}>题库列表</Button>}
            data-testid="card-banks"
          >
            <Paragraph type="secondary">
              管理词汇库、词表导入、歧义消歧、版本发现与质量追踪。
            </Paragraph>
          </Card>
        </Col>

        <Col xs={24} sm={12}>
          <Card
            hoverable
            title={<Space><AppstoreOutlined style={{ color: "#722ed1" }} />共享组件沙盒</Space>}
            extra={<Button onClick={() => onNavigate("/components")}>查看组件</Button>}
            data-testid="card-components"
          >
            <Paragraph type="secondary">
              W00 共享纯展示组件沙盒、IndexedDB Outbox 持久队列与键盘导航验证。
            </Paragraph>
          </Card>
        </Col>
      </Row>
    </div>
  );
};
