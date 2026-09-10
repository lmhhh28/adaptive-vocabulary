# 跨平台客户端实现指南

## 1. 概述
- 本指南旨在帮助开发者在任何平台上构建 Adaptive Vocabulary 的原生客户端。
- 后端在用户机器上本地运行，地址为 `127.0.0.1:8080`。
- 所有客户端通过统一的 HTTP JSON API 与后端进行通信。
- 完整的 API 细节与数据结构，请参考 `backend-api-reference.md`。

## 2. 平台无关架构设计

### 2.1 建议分层
```text
UI Layer (Platform-specific)
   │
   │ ViewModel / State Management
   │
   ├── PracticeViewModel
   ├── ExamViewModel  
   ├── BankViewModel
   └── ImportViewModel
   │
   │ API Client Layer
   │
   ├── SessionManager (CSRF + Cookie)
   ├── IdempotencyManager
   └── APIClient (HTTP wrapper)
   │
   │ Local Storage
   │
   ├── PendingOperationStore
   └── StateCache
```

### 2.2 核心组件职责

#### SessionManager
- 管理 `avocab_session` cookie 的生命周期。
- 存储 CSRF 令牌。
- 在遇到 `403 CSRF_REJECTED` 错误时自动处理令牌刷新。
- 在非浏览器平台上：需要手动实现 cookie 的拦截和持久化 (cookie jar)。

#### IdempotencyManager  
- 为每个用户的写入操作生成 UUID v4。
- 将 key、path 和 body 捆绑存储为 `PendingOperation`。
- **永远不**为相同操作的重试生成新的 key。
- 跟踪并记录哪些操作已完成。

#### APIClient
- 基础 URL: `http://127.0.0.1:8080`
- 读取请求 Headers: `Accept: application/json`
- 写入请求 Headers: `Accept: application/json`, `Content-Type: application/json`, `X-CSRF-Token`, `Idempotency-Key`, `Cookie`
- 响应解析：检查 HTTP 状态码，在非 2xx 响应时解析并抛出包含 JSON 错误体的异常。
- 超时设置：常规请求建议 30 秒，考试交卷等重要操作建议 60 秒。

#### PendingOperationStore
- 持久化未完成的操作，以在应用重启后依然存活。
- 对于考试事件：维护严格的有序队列。
- 每个条目包含: `{path, body, key, timestamp, status}`
- 状态枚举: `pending | inflight | confirmed | failed`

## 3. 认证实现

### 3.1 Cookie 管理

客户端应用启动及首次请求处理逻辑（伪代码）：
```
function initializeSession() {
  response = GET /client-context
  setCookieHeader = response.headers["Set-Cookie"]
  avocab_session = extractCookie(setCookieHeader, "avocab_session")
  persistentStore.save("cookie", avocab_session)
}

function interceptRequest(request) {
  cookie = persistentStore.get("cookie")
  if cookie {
    request.headers["Cookie"] = "avocab_session=" + cookie
  }
  return request
}
```

平台特定提示：
- **iOS (Swift)**: 使用 `URLSession` 结合 `HTTPCookieStorage.shared`，或者实现自定义存储。
- **Android (Kotlin)**: 使用 OkHttp 的 `CookieJar` 接口，或 `HttpURLConnection` 的 `CookieManager`。
- **Flutter (Dart)**: 使用 `dio` 库配合 `CookieManager` 拦截器。
- **React Native**: 使用 `react-native-cookies`，或在使用 `fetch` 时设置 `credentials: 'include'`。
- **Desktop (Electron)**: 使用 Electron 提供的 `session` cookie 管理 API。
- **Desktop (Tauri)**: 使用启用了 `cookie_store` feature 的 `reqwest` 客户端。

### 3.2 CSRF Token 管理
- 在应用启动时以及遇到 `403 CSRF_REJECTED` 错误后拉取新的令牌 (`GET /client-context`)。
- 令牌存储在内存中（不需持久化，因为服务端重启会导致令牌失效）。
- 令牌格式: `<seconds>.<hmac>`，有效期通常为 24 小时。
- 在所有写入请求（POST, PUT, DELETE, PATCH）中通过 `X-CSRF-Token` 头部注入。

## 4. 幂等性实现

详细实现指南：
1. **Key 生成**: 在每个平台上使用等效的 `crypto.randomUUID()` 函数生成 UUID v4。
2. **Key 绑定**: 每次用户发起一个独立动作时分配一个唯一的 key，并将其与请求的 path 和 body 一同持久化。
3. **重试策略**: 如果发生网络超时，客户端应该重发具有相同 key、path 和 body 的请求。
4. **特殊情况**:
   - 考试的响应事件 (`response-events`): 使用 `client_event_id` 作为幂等性 key，无需再在 Header 中提供。
   - 考试交卷 (`submit`): 使用 `submission_id` 作为幂等性 key。
   - 练习模式进入下一题 (`next`): 使用基于 cursor 的幂等性控制，不需要 Header 传递 key。
5. **严禁**: 永远不要为了重试一个失败的请求而生成新的 key。
6. **严禁**: 永远不要在没有用户明确操作的情况下自动重试写入逻辑（除为了恢复队列的一致性操作外）。

各平台生成 UUID 示例：
- Swift: `UUID().uuidString.lowercased()`
- Kotlin: `java.util.UUID.randomUUID().toString()`
- Dart: `const Uuid().v4()`
- JavaScript: `crypto.randomUUID()`

## 5. 练习模式实现

状态机定义：
`Idle -> SessionCreated -> QuestionDisplayed -> AnswerSubmitted -> FeedbackShown -> (循环或结束)`

关键实现细节：
1. **Cursor 管理**: 从 `0` 开始。在收到反馈后，使用响应中的 `next_cursor` 进行下一次 `next` 请求。
2. **处理 unavailable**: 提示原因（例如“所有词汇均已掌握”），并提供结束练习的选项。这**不是**错误，是正常的业务流程。
3. **处理 ALREADY_ANSWERED**: 如果重复提交同一个 cursor 的答案，服务端会返回该错误。直接使用返回数据中附带的已有反馈即可。
4. **处理 REVISION_CONFLICT on next**: 使用服务端返回 details 中的正确 cursor 继续请求。
5. **处理 SESSION_ACTIVE**: 启动新练习时遇到该错误，说明有正在进行的会话，应向用户展示“恢复练习”选项。
6. **应用重启恢复**: 重启后请求 `GET /sessions/active`，如果有数据，再通过 `GET /practice-sessions/{id}` 恢复现场。

## 6. 考试模式实现

这是客户端最复杂的流程。必须保证离线可用性和最终一致性。

### 6.1 事件队列设计
数据结构与状态：
```json
ExamEventQueue:
  items: [{
    client_event_id: string,
    client_sequence: int,
    instance_id: string,
    action: string,
    option_id?: string,
    expected_revision: int,
    status: "pending" | "inflight" | "confirmed" | "failed"
  }]
  currentSequence: int (从 0 开始, 第一个事件使用 1)
  lastAckSequence: int (来自服务端)
  currentRevision: int (来自服务端)
```

操作流程：
1. `enqueue(action)`: 生成包含下个序列号和当前本地 revision 的事件加入队尾。
2. `sendHead()`: 如果队首事件状态为 pending，发送请求，并标记为 inflight。
3. `onSuccess(response)`: 更新本地的 revision 和 ack，将队首事件移除，继续发送下一个。
4. `onError(error)`: 根据不同的错误代码走不同的恢复分支。

### 6.2 错误恢复流程

#### REVISION_CONFLICT (且 acceptance=not_accepted)
1. 发起 `GET /api/v1/exams/{id}` 获取当前服务端的真实状态。
2. 同步并更新本地的 `revision` 和 `ack_sequence`。
3. 对失败的事件进行 Rebase：保留原有的 `client_event_id` / `instance_id` / `action` / `option_id`，但更新其 `expected_revision` 和 `client_sequence`。
4. 增加 `rebased_from` 字段说明变更。
5. 最多自动 Rebase 3 次，超过则挂起队列，要求用户确认。

#### EVENT_SEQUENCE_GAP
1. 发起 `GET /api/v1/exams/{id}` 同步状态。
2. 调整本地的 sequence 计数器以匹配服务端期望。

#### 网络错误（超时/断网）
1. 恢复连接后，检查该事件是否已被接收：发起 `GET /api/v1/exams/{id}/response-events/{client_event_id}`。
2. 如果返回 `200`: 事件已被服务端接收，更新本地状态，出队并处理下一个。
3. 如果返回 `404`: 事件可能丢失，使用**原封不动**的 body 重试。
4. **永远不要**假设网络超时意味着服务端没有处理该事件。

#### 队列丢失 (App 崩溃, 内存被清)
1. 发起 `POST /api/v1/exams/{id}/telemetry-gaps` 携带当前的 revision。
2. 响应体将包含服务端的 `last_ack_sequence`。
3. 重置本地队列的当前序号，从 `ack_sequence + 1` 继续。

### 6.3 交卷流程
1. 确保事件队列已被完全确认（所有队列中的 items 都必须是 confirmed 或清空）。
2. 构建 submit 请求，包含当前的 `revision`，生成的 `submission_id` 以及最新的 `last_ack_sequence`。
3. 发起 `POST /api/v1/exams/{id}/submit`。
4. 如果遇到 `EXAM_SUBMITTED`（因超时被服务端自动交卷），停止流程，直接读取结果。
5. 最终通过 `GET /api/v1/exams/{id}/result` 展示成绩。

### 6.4 倒计时实现
- `deadline` 是一个 RFC 3339 字符串，由创建考试的响应下发。
- 客户端的倒计时纯粹是为了 UI 展示。**服务端的 deadline 是唯一权威的时间**。
- 本地倒计时结束后，客户端应尝试自动发起交卷。
- 如果在 deadline 过后仍在发送 `response-events`，服务端将返回 `409 EXAM_SUBMITTED`。

## 7. 导入功能实现

### 7.1 三种导入方式
1. JSON 词汇数组
2. JSON 文本 (按行分隔)
3. 文件上传 (使用 `multipart/form-data`)

### 7.2 消歧处理流程
1. 检查导入的返回结果，若 `ambiguous_count > 0`，进入消歧界面。
2. 获取 status=ambiguous 的条目进行分页展示。
3. 为每一项向用户展示 `candidates` 候选义项。
4. 用户选择要采纳或排除的 `sense`。
5. 发起解析请求：`POST` resolution 包含 `sense_refs` (由 `sense_id` 构成的数组)。

## 8. 维护状态处理

- 客户端应定期轮询 `GET /api/v1/maintenance`。
- 当 `mode=rebuilding` 时：禁用界面上**所有**涉及写入数据的操作。
- 通过 `run_status` 字段向用户展示当前的维护进度。
- 当 `mode=active` 恢复后，客户端解禁 UI 操作。

## 9. 平台特定示例

### 9.1 Swift (iOS/macOS)
```swift
import Foundation

class APIClient {
    static let shared = APIClient()
    private let baseURL = URL(string: "http://127.0.0.1:8080")!
    private var csrfToken: String?
    
    func fetchContext() async throws {
        var request = URLRequest(url: baseURL.appendingPathComponent("/client-context"))
        let (data, response) = try await URLSession.shared.data(for: request)
        // Extract CSRF and Handle Cookies transparently via HTTPCookieStorage
    }
    
    // Example: Exam Event Queue Enqueue
    func enqueueEvent(examId: String, action: String, expectedRevision: Int) {
        let eventId = UUID().uuidString.lowercased()
        let payload: [String: Any] = [
            "client_event_id": eventId,
            "action": action,
            "expected_revision": expectedRevision
        ]
        // Save to persistent storage and trigger send
    }
}
```

### 9.2 Kotlin (Android)
```kotlin
import okhttp3.*
import java.util.UUID

class APIClient {
    private val client = OkHttpClient.Builder()
        .cookieJar(MyCookieJar())
        .build()
        
    fun submitExamEvent(examId: String, payload: String) {
        val request = Request.Builder()
            .url("http://127.0.0.1:8080/api/v1/exams/$examId/response-events")
            .post(RequestBody.create(MediaType.parse("application/json"), payload))
            .addHeader("X-CSRF-Token", getCsrfToken())
            .build()
            
        client.newCall(request).enqueue(object : Callback {
            override fun onFailure(call: Call, e: IOException) {
                // Handle network failure -> Check with GET if event was accepted
            }
            override fun onResponse(call: Call, response: Response) {
                // Handle 200 or 409 REVISION_CONFLICT
            }
        })
    }
}
```

### 9.3 Dart (Flutter)
```dart
import 'package:dio/dio.dart';
import 'package:uuid/uuid.dart';

class APIClient {
  final Dio dio = Dio(BaseOptions(baseUrl: "http://127.0.0.1:8080"));
  
  Future<void> answerPractice(String sessionId, String optionId, int cursor) async {
    try {
      final response = await dio.post(
        '/api/v1/practice-sessions/$sessionId/next',
        data: {
          'option_id': optionId,
          'cursor': cursor
        },
        options: Options(headers: {'X-CSRF-Token': csrfToken})
      );
      // Handle success
    } on DioException catch (e) {
      if (e.response?.statusCode == 409) {
         // Parse JSON to handle ALREADY_ANSWERED or REVISION_CONFLICT
      }
    }
  }
}
```

### 9.4 TypeScript (React Native / Web)
在 React Native 中，你可以很大程度上复用已有的 `frontend-client.mjs`。主要适配点在于：
- React Native 的 `fetch` 需要通过 `credentials: 'include'` 或引入第三方 Cookie 库来管理状态。
- 使用 `react-native-get-random-values` 和 `uuid` 来实现 `crypto.randomUUID()` 功能。
- 由于在手机端运行，当应用切到后台再返回时，应该主动刷新状态并检查 `maintenance` 模式。

## 10. 测试清单

在发布您的客户端前，请使用此清单进行全面测试：
- [ ] CSRF 生命周期测试 (初始获取, 过期表现, 403 后自动刷新)
- [ ] Cookie 在应用完全退出重启后是否持续存在
- [ ] 幂等性 key 是否被正确管理且不被滥用于重试
- [ ] 练习模式：创建、回答、无可用词汇处理、主动结束、从活跃会话恢复
- [ ] 考试模式：创建、事件排队与发送、Rebase 冲突恢复、间隔修复(Gap)、交卷、倒计时与超时处理
- [ ] 词库导入：JSON 数组支持、JSON 行支持、多部分文件上传、消歧界面交互
- [ ] 维护模式：界面是否能在 rebuilding 时禁用输入，active 后恢复
- [ ] 应对所有 HTTP 409 自定义错误代码的逻辑分支覆盖
- [ ] 模拟断网与网络中断后的事件状态恢复
- [ ] 零值处理：`cursor=0`, `revision=0` 的边界情况
- [ ] Null 值处理：能正确解析返回结果中为 null 的字段 (如 `ratio`, `first_correct`)
- [ ] 时间戳转换：确认客户端能无损处理微秒精度的 `updated_at` 时间戳
