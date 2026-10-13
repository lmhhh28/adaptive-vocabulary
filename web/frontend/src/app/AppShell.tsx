import React, { useEffect, useState } from "react";
import { Layout, Menu, Typography, Button } from "antd";
import { ReadOutlined, FormOutlined, DatabaseOutlined, AppstoreOutlined, HomeOutlined, UploadOutlined, ThunderboltOutlined, SafetyCertificateOutlined } from "@ant-design/icons";
import { HomeView } from "./HomeView";
import { ComponentsDemoView } from "./ComponentsDemoView";
import { MaintenanceBanner } from "./MaintenanceBanner";
import { fetchClientContext } from "../api/clientContext";
import { request } from "../api/transport";

const { Header, Content, Footer } = Layout;
const { Title, Paragraph } = Typography;

export interface AppModuleDescriptor {
  id: string;
  route: string;
  title: string;
  Component: React.ComponentType<{ onNavigate?: (path: string) => void; currentPath?: string }>;
}

const featureModulesRecord = import.meta.glob<{ default?: AppModuleDescriptor; module?: AppModuleDescriptor; practiceModule?: AppModuleDescriptor }>(
  "../features/*/module.tsx",
  { eager: true }
);

const featureModules: AppModuleDescriptor[] = Object.values(featureModulesRecord)
  .map((m) => m.default || m.module || m.practiceModule)
  .filter((m): m is AppModuleDescriptor => Boolean(m && m.route && m.Component));

export const AppShell: React.FC = () => {
  const [currentPath, setCurrentPath] = useState<string>(
    (window.location.pathname || "/") + (window.location.search || "")
  );
  const [maintenance, setMaintenance] = useState<any>(null);

  useEffect(() => {
    fetchClientContext().catch(() => {});

    request("/api/v1/maintenance")
      .then((data) => setMaintenance(data))
      .catch(() => {});

    const handlePopState = () => {
      setCurrentPath((window.location.pathname || "/") + (window.location.search || ""));
    };
    window.addEventListener("popstate", handlePopState);
    return () => window.removeEventListener("popstate", handlePopState);
  }, []);

  const handleNavigate = (path: string) => {
    const fullCurrent = (window.location.pathname || "/") + (window.location.search || "");
    if (fullCurrent !== path) {
      window.history.pushState({}, "", path);
    }
    setCurrentPath(path);
  };

  const navMenuItems = [
    { key: "/", icon: <HomeOutlined />, label: "首页" },
    { key: "/practice", icon: <ReadOutlined />, label: "自适应练习" },
    { key: "/exams", icon: <FormOutlined />, label: "阶段考试" },
    { key: "/banks", icon: <DatabaseOutlined />, label: "题库与内容" },
    { key: "/imports", icon: <UploadOutlined />, label: "词表导入" },
    { key: "/generation", icon: <ThunderboltOutlined />, label: "内容生成" },
    { key: "/quality", icon: <SafetyCertificateOutlined />, label: "质量与维护" },
    { key: "/components", icon: <AppstoreOutlined />, label: "共享组件" },
  ];

  let activeNav = "/";
  if (currentPath.startsWith("/practice")) activeNav = "/practice";
  else if (currentPath.startsWith("/exam")) activeNav = "/exams";
  else if (currentPath.startsWith("/bank")) activeNav = "/banks";
  else if (currentPath.startsWith("/import")) activeNav = "/imports";
  else if (currentPath.startsWith("/generation")) activeNav = "/generation";
  else if (currentPath.startsWith("/quality")) activeNav = "/quality";
  else if (currentPath.startsWith("/components")) activeNav = "/components";

  const matchedModule = featureModules.find((m) => currentPath.startsWith(m.route));

  return (
    <Layout style={{ minHeight: "100vh", backgroundColor: "#f5f5f5", overflowX: "hidden", maxWidth: "100%" }} data-testid="app-shell">
      <Header
        style={{
          position: "sticky",
          top: 0,
          zIndex: 100,
          display: "flex",
          alignItems: "center",
          borderBottom: "1px solid #f0f0f0",
          backgroundColor: "#ffffff",
          padding: "0 16px",
          height: 56,
          maxWidth: "100%",
          overflow: "hidden",
        }}
      >
        <div
          style={{
            cursor: "pointer",
            marginRight: 16,
            display: "flex",
            alignItems: "center",
            flexShrink: 0,
          }}
          onClick={() => handleNavigate("/")}
          data-testid="header-logo"
        >
          <Title level={4} style={{ margin: 0, color: "#1677ff", fontWeight: 600, whiteSpace: "nowrap" }}>
            Adaptive Vocabulary
          </Title>
        </div>

        <Menu
          mode="horizontal"
          selectedKeys={[activeNav]}
          items={navMenuItems}
          onClick={({ key }) => handleNavigate(key)}
          style={{ flex: 1, minWidth: 0, borderBottom: "none", height: 56, lineHeight: "56px" }}
          data-testid="nav-menu"
        />
      </Header>

      <MaintenanceBanner
        mode={maintenance?.mode}
        activeVersion={maintenance?.active_version}
        requiresRebuild={maintenance?.requires_rebuild}
      />

      <Content style={{ padding: "0 16px", minHeight: "calc(100vh - 120px)", overflowX: "hidden", maxWidth: "100%" }}>
        {activeNav === "/" && <HomeView onNavigate={handleNavigate} />}
        {activeNav === "/components" && <ComponentsDemoView />}
        {matchedModule && activeNav !== "/" && activeNav !== "/components" && (
          <matchedModule.Component onNavigate={handleNavigate} currentPath={currentPath} />
        )}
        {!matchedModule && activeNav !== "/" && activeNav !== "/components" && (
          <div style={{ maxWidth: 800, margin: "60px auto", textAlign: "center", padding: 40, backgroundColor: "#fff", borderRadius: 4 }}>
            <Title level={3}>功能模块待接入</Title>
            <Paragraph type="secondary">
              当前路径 <code>{currentPath}</code> 属于后续规划模块（W01–W07）。W00 基础框架与共享组件已就绪。
            </Paragraph>
            <Button type="primary" onClick={() => handleNavigate("/")}>
              返回首页
            </Button>
          </div>
        )}
      </Content>

      <Footer style={{ textAlign: "center", color: "#8c8c8c", fontSize: 13, padding: "16px 24px" }}>
        Adaptive Vocabulary Web · Ant Design v5 · Design v1.1
      </Footer>
    </Layout>
  );
};
