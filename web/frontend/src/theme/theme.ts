import type { ThemeConfig } from "antd";

export const themeConfig: ThemeConfig = {
  token: {
    colorPrimary: "#1677ff",
    colorInfo: "#1677ff",
    colorSuccess: "#52c41a",
    colorWarning: "#faad14",
    colorError: "#ff4d4f",
    colorBgLayout: "#f5f5f5",
    colorBgContainer: "#ffffff",
    colorText: "#1f1f1f",
    colorTextSecondary: "#595959",
    borderRadius: 4,
    wireframe: false,
  },
  components: {
    Layout: {
      headerBg: "#ffffff",
      headerHeight: 56,
      headerPadding: "0 24px",
    },
    Card: {
      paddingLG: 20,
    },
    Button: {
      borderRadius: 4,
    },
  },
};
