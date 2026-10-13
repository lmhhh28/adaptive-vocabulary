import React from "react";
import ReactDOM from "react-dom/client";
import { ConfigProvider, App } from "antd";
import zhCN from "antd/locale/zh_CN";
import { themeConfig } from "./theme/theme";
import { AppShell } from "./app/AppShell";

const rootElement = document.getElementById("root");
if (rootElement) {
  ReactDOM.createRoot(rootElement).render(
    <React.StrictMode>
      <ConfigProvider locale={zhCN} theme={themeConfig}>
        <App>
          <AppShell />
        </App>
      </ConfigProvider>
    </React.StrictMode>
  );
}
