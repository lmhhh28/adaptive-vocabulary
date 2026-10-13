import React from "react";
import { Alert } from "antd";

export interface MaintenanceBannerProps {
  mode?: string;
  activeVersion?: string;
  requiresRebuild?: boolean;
}

export const MaintenanceBanner: React.FC<MaintenanceBannerProps> = ({
  mode,
  activeVersion,
  requiresRebuild,
}) => {
  if (mode !== "maintenance" && mode !== "rebuilding" && !requiresRebuild) {
    return null;
  }

  return (
    <Alert
      message="系统处于维护模式"
      description={`当前处于投影重建或系统维护状态 (版本: ${activeVersion || "未知"})，所有学习写入操作已暂时锁定，只读浏览正常可用。`}
      type="warning"
      banner
      closable={false}
      data-testid="maintenance-banner"
    />
  );
};
