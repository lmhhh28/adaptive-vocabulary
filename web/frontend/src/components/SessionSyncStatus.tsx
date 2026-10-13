import React from "react";
import { Tag, Tooltip } from "antd";
import { SyncOutlined, CheckCircleOutlined, ExclamationCircleOutlined } from "@ant-design/icons";

export interface SessionSyncStatusProps {
  pendingCount: number;
  isStorageAvailable?: boolean;
  hasUnknown?: boolean;
}

export const SessionSyncStatus: React.FC<SessionSyncStatusProps> = ({
  pendingCount,
  isStorageAvailable = true,
  hasUnknown = false,
}) => {
  if (!isStorageAvailable) {
    return (
      <Tooltip title="浏览器本地持久化不可用，作答事件无法离线保存">
        <Tag color="error" icon={<ExclamationCircleOutlined />} data-testid="storage-unavailable-tag">
          存储不可用
        </Tag>
      </Tooltip>
    );
  }

  if (hasUnknown) {
    return (
      <Tooltip title="有网络结果未确认的操作，正在按原请求恢复">
        <Tag color="warning" icon={<ExclamationCircleOutlined />} data-testid="sync-unknown-tag">
          待确认
        </Tag>
      </Tooltip>
    );
  }

  if (pendingCount > 0) {
    return (
      <Tag color="processing" icon={<SyncOutlined spin />} data-testid="sync-pending-tag">
        同步中 ({pendingCount})
      </Tag>
    );
  }

  return (
    <Tag color="success" icon={<CheckCircleOutlined />} data-testid="sync-ok-tag">
      已同步
    </Tag>
  );
};
