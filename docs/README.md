# 文档索引

## 跨平台前端开发

| 文档 | 用途 |
| --- | --- |
| [后端接口完整参考（跨平台适配）](backend-api-reference.md) | 全部接口的完整定义、数据类型、安全认证、错误码、幂等性及跨平台实现指南 |
| [跨平台客户端实现指南](cross-platform-client-guide.md) | iOS/Android/Flutter/React Native/Desktop 客户端架构、认证、事件队列及平台示例代码 |
| [数据库 Schema 完整参考](database-schema-reference.md) | 全部数据库表结构、字段约束、索引、实体关系图及不变量 |

## 当前实现与联调

| 文档 | 用途 |
| --- | --- |
| [前端接口联调指南](frontend-api.md) | 初始化、CSRF、错误、并发重试、页面流程、已知实现差异 |
| [前端接口与字段参考](frontend-api-reference.md) | 39 个业务 HTTP 方法/路径组合及请求响应字段 |
| [浏览器请求示例](examples/frontend-client.mjs) | 固定写操作正文与 key、CSRF、统一错误；不自动重试 |
| [真实进程联调演练](examples/frontend-api-walkthrough.py) | 在临时数据目录验证文档请求，可选 Chromium 验证 JS helper |
| [运行与个人使用手册](runbook.md) | 启动、日常操作、维护、备份与恢复 |
| [后端真实进程回归报告](e2e-backend-regressions.md) | 测试矩阵、已修复问题和验证边界 |
| [生产验收记录](production-acceptance.md) | 已记录的生产来源与模型验收证据；不等于变更后的重新授权 |

## 设计基线

[00 冻结设计](00-frozen-design.md)、[05 API 与验证设计](05-api-and-validation.md)、[08 HTTP v1 冻结契约](08-http-contract.md)、[09 实施计划](09-implementation-plan.md)、[10 审查修订](10-review-resolutions.md)属于版本化设计基线。其“待实现”表述是历史状态，当前调用方式及偏差以联调文档的核对日期和代码证据为准。

前端文档的补充不重新封存或更改冻结文件哈希。设计变更另按冻结流程处理。
