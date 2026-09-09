# 1.1.0 独立审查修订与验收契约

状态：2026-09-08 冻结。本文与00、08、policy-v2共同定义修订后的规则；本文中的状态转移和资格定义优先于01–07的概述。这里冻结的是实现要求，业务E2E尚待各任务交付。实施任务见[09](09-implementation-plan.md)。

## 1. 考试事件重试与多标签收敛（R01）

客户端每个未完成用户动作持久保存client_event_id、action、instance_id、option_id、完整待发送正文，以及状态pending/unknown/rejected/acknowledged。首次发送前写入本地持久队列；不能只存在组件内存中。服务端的全卷序号仍连续，成功的实质选择/clear/noop均递增revision。

| 结果 | 接受状态 | 后续动作 |
| --- | --- | --- |
| 成功或原请求幂等重放 | accepted | 保存返回水位，移出待确认队列 |
| 网络中断、超时、响应不可解析 | unknown | 原ID、原序号、原revision和原正文重试，不重排 |
| REVISION_CONFLICT / EVENT_SEQUENCE_CONFLICT / EVENT_SEQUENCE_GAP，且error.details.acceptance=not_accepted | 明确未接受 | 恢复快照；保留用户动作和client_event_id，按新revision与下一序号重新发送 |
| IDEMPOTENCY_CONFLICT | 不能推断原事件未接受 | 停止自动改写该事件；读取事件回执，核对已接受动作 |
| EXAM_SUBMITTED / SESSION_CLOSED | 终态 | 待确认动作标为未保存，读取最终服务器结果；不再提交 |

只有服务端已经在写事务中确认该client_event_id没有已接受结果，并明确拒绝此次请求，才能返回acceptance=not_accepted。唯一序号被另一ID占用返回EVENT_SEQUENCE_CONFLICT；禁止把另一事件的成功结果当成本事件回执。未知接受状态不能凭一次查询404推断未接受，因为原请求可能仍在途。

同一事务决定“明确拒绝且不写idempotency_record”或“成功写事件和幂等结果”，不存在已持久化业务拒绝占用键的第三条路径。摘要冲突只针对实际存在的成功记录；不能为失败请求预先建立永久摘要绑定。这与08的仅成功保存规则一致，不允许把任意409都当成not_accepted。

明确拒绝的请求不占用幂等键，因此重排后可复用client_event_id/Idempotency-Key并改变client_sequence、expected_revision；action/instance/option不变，附rebased_from={client_sequence,expected_revision}留痕。后续一旦接受，新正文成为该键唯一合法重试正文。每个动作最多自动重排3次，再冲突则保留待保存状态并提供显式重试；不静默丢弃或显示已保存。

GET /exams/{id}/response-events/{client_event_id}只返回已接受的event_id、client_event_id、client_sequence、accepted_revision、instance_id、action、option_id或404，不含正确性。恢复后如能取得回执，按服务器结果确认该动作。

队列丢失时先GET考试快照，再提交telemetry-gaps的revision CAS屏障。成功时原子设置telemetry_incomplete、递增revision并返回水位；屏障之后，尚未接受的旧revision请求只能被拒绝，已接受请求仍可幂等读取。屏障冲突先恢复再重试；成功前不发送新选择。其后从last_ack_sequence+1开始，成绩只使用服务器草稿。

session.revision持久化且同一session永不回退/重置；response-event在同一写事务中要求expected_revision严格等于当前值。因此屏障提交后的旧revision即使服务重启也不能被接受，不需要第二套epoch。gap操作保存自身幂等回执及事件水位；新的gap只能再次递增，不能重新开放旧请求。

验收EX-02：两标签在revision=10、sequence=7同时发送不同的8号事件；一个接受，另一个明确拒绝后重排为9，两个动作各保存一次。另测接受后丢响应只生成一个事件、未知请求不重排、gap屏障阻断迟到旧请求、连续三次冲突保留未保存提示。

## 2. 外部调用预算账本（R02）

Go新增generation_call及generation_budget_ledger，业务库是唯一预算账本，checkpoint不承担额度权威。logical_call_key由item_id、attempt_number、stage(generator/evaluator)、candidate_index、evaluation_index组成；network_attempt_id是worker生成的UUID，每一次实际HTTP发送必须有不同的ID。reservation_id由服务器生成。该worker UUID是业务ID规则的显式例外。

调用前POST /internal/v1/generation/calls/reserve，带租约身份、logical_call_key、network_attempt_id、profile/config版本、input_sha256、input_token_estimate和max_output_tokens。单写事务内校验有效租约、固定输入/profile、该逻辑调用最多3次网络尝试，以及整个job的请求数/token余额，原子增加一次请求占用和本次token预留，再返回reservation_id。余额不足返回BUDGET_EXHAUSTED，不产生授权。并发item共享同一job账本。

预留记录永久保留，状态reserved → settled | unknown；请求占用不返还。token占用为所有settled实际usage加所有reserved/unknown预留之和。缺少usage按预留settled，已结算正文不同报冲突。业务拒绝/网络失败不伪造候选，但已批准的尝试占用仍保留，这是防止未知消耗被重复使用的保守取舍。

reserve的网络重试可用原幂等键取回同一预留，但**重放预留响应不授权重放供应商副作用**。外部HTTP客户端关闭隐式重试、自动重定向和SDK自动重试；每次新发送先申请新network_attempt_id。工作进程重启、接管或换fence后，未结算预留一律视为unknown，不复用它发送请求；后续尝试重新占用额度并遵守总计数。

Go启动恢复和claim接管事务将已过期/被作废fence的reserved调用条件更新为unknown，并保留预留；这与递增fence/授予新租约同事务完成。进程恢复不重用原worker incarnation：等待旧租约到期或显式释放后重新claim。仍持有效租约的worker如果确定未发送就取消，可settle(network_error, usage缺失)，仍按预留扣减；不能证明供应商未收到则settle(unknown)。停在reserved不会释放额度；过期即归入unknown，恢复不依赖扫表定时器侥幸执行。

POST /calls/{reservation_id}/settle带原预留身份、provider_request_id?、response_sha256?、outcome=response/network_error/unknown、usage?。有确定usage时将预留替换为实际值，缺失时保守扣减；unknown允许日后一次确定结算。它只对账，不发布内容。匹配原预留的迟到结算可在租约过期或维护期间入账；仍需worker认证和原owner/fence/input核对。候选接收与发布始终要求当前有效租约。更晚的重复结算不得重复扣费或更改已结算值。

数据库对每个reservation_id只允许一条最终settlement，保存规范化settlement_digest。reserved→unknown、reserved→settled、unknown→settled及余额变化均以原状态条件更新并在同一写事务完成；恢复和结算竞争时重读当前状态。同ID同最终摘要返回原结果，不同摘要返回CALL_SETTLEMENT_CONFLICT且不更改任何账项；已settled不能回退unknown，也不能用不同幂等键绕过唯一最终结算。provider_request_id缺失允许，但存在时参与摘要核对。

输入token是估计值：本地强制限制的是调用授权数与已知/预留占用，不能承诺供应商账单绝对不超估计。若实际usage高于预留，完整记账、标记budget_overrun并停止该job新增授权；不截断usage伪造未超预算。供应商费用上限由显式生产配置另行约束。

候选metadata必须关联generator及两次evaluator的reservation_id，Go验证阶段、输入摘要、调用结果和检查对应关系。候选不重复记usage；超预算或来源缺失不发布。备份包括全部预留和对账记录；恢复未完成租约作废后，未结算预留保持unknown并继续占用，不因缺checkpoint归零。

验收GE-02：只剩一次额度、两个item竞争，最多一个获得授权；供应商收到请求后worker崩溃，恢复不复用旧预留；usage缺失、迟到结算、响应丢失、三次网络尝试上限、actual超过估计和无checkpoint恢复均可核对账本。

## 3. 重建中止与恢复（R03）

model_rebuild_run新增kind=policy_only/evidence_revision、base_active_version、base_revision_digest、target_policy、fixed_watermark、status以及requires_rebuild。projection_control仍只有active/rebuilding两种mode；它与事实修订必须在同一业务库事务中切换。

| 运行与操作 | run结果 | 维护闸门 | 允许的下一步 |
| --- | --- | --- | --- |
| 成功构建并验证 | succeeded | 原子切换新投影后解除 | 正常运行 |
| 失败或崩溃 | failed或待恢复running | 保持 | resume同一run，或abort |
| policy_only执行abort，且事实修订摘要仍等于基线 | aborted | 回到原active_version并解除 | 正常运行 |
| evidence_revision执行abort，或当前修订摘要与基线不一致 | aborted_maintenance | 保持rebuilding，requires_rebuild=true | restart创建新run和新目标version |

abort不删除/撤销已写event_revision，也不把旧投影解释为已经应用新修订。aborted_maintenance时，旧投影只可带maintenance/stale标志读出，不能用于发题或接受学习写入。restart继承待应用处置集合并固定新run身份，在同一维护期构建一致投影；成功验证才解除闸门。已完成或已abort的run不可resume；restart不复用失败目标version。

所有运行转换在单写事务中同时核对projection_control.mode/run_id/active_version以及run.status；成功切换、abort、resume与restart采用条件更新，任一前提不满足则REBUILD_STATE_CONFLICT，不能部分变更。policy_only解闸还要求active_version==base_active_version；已切换成功的run不可再abort回旧投影。写库CLI共用09的独占进程锁，因此两个CLI或CLI与服务不能同时修改同一库。

重复abort返回已保存终态；重复resume只恢复同一run，不新建第二个执行者。restart在数据库对predecessor_run_id建立唯一后继关系：相同前序和目标version返回原新run，不同目标冲突；若该后继再失败/中止，只能对新的run进行后续恢复。重复CLI不生成额外修订或重复投影切换。

CLI：avocab learning rebuild resume --run <id>；avocab learning rebuild abort --run <id>；avocab learning rebuild restart --run <id> --projection <new-version>。返回run_id、status、mode、active_version、requires_rebuild、next_action。进程退出码0表示操作成功执行，不等于服务已解除维护；调用方必须检查mode/requires_rebuild。

验收RB-02：invalidate落库后故障中止，abort后写入口继续MAINTENANCE；重启仍保持闸门；restart成功后才开放，失效题影响从证据/FSRS/混淆中排除。纯策略重建abort另测可以安全恢复旧投影。

## 4. 曝光时间边界（R04）

所有exposure保存source_instance_id、kind、服务器observed_at及全局event_sequence。实际展示另存客户端时间，但不改变事实排序。当前观测cutoff为普通/纠错attempt的服务器接收时间和事件序号，或合格考试唯一有效select的服务器时间和序号。

派生same_form_reserved与recent_related_exposure只读取source_instance_id不同于当前实例、且(observed_at,event_sequence)严格早于cutoff的曝光。same_form_reserved不设24小时窗口，recent_related_exposure有24小时窗口；当前题自身reserved不算历史重复，本次feedback_available也不能反向污染本次观测。保留原曝光事实，排除只作用于当前标签查询。

反馈产生在评分结果可取得之后：训练提交事务追加本实例唯一feedback_available；考试交卷事务为每题追加一次feedback_available，时间为交卷时刻。它们只用于后续观测，不代表实际展示；结果重读不重复追加。考试批量reserved中其他实例的相关曝光仍按保守规则计入，本实例必须排除。回放按相同cutoff查询，不按重建时刻/交卷处理时刻查询全部历史。

验收OB-01：首次遇到某词无历史提示；第二个不同实例同form命中永久重复标签；另form同lexeme在24小时内命中近期提示；24小时外只保留同form标签；本次反馈和考试选择之后的曝光不影响该次观测；在线与回放标签完全一致。

## 5. 提前复习与普通证据资格矩阵（R05）

“普通位置”定义为所有非纠错训练位置，normal及early_review成功保留均推进normal_position；事后失效不回退历史cursor/ordinal/normal_position，也不改变已发题的探索位置。下面矩阵作用于当前有效证据及选题用间隔计数，机器来源为policy-v2.observation_eligibility。

| 观测 | 普通证据 | 主混淆机会/错选 | 更新待巩固 | 推进普通5词间隔 | 推进纠错3词间隔 | FSRS |
| --- | --- | --- | --- | --- | --- | --- |
| normal训练 | 是 | 是 | 是 | 是 | 是 | Good/Again |
| early_review训练 | 是 | 是 | 是 | 是 | 是 | Good/Again |
| correction训练 | 仅纠错组 | 仅纠错组 | 否 | 否 | 是 | Good/Again |
| 可靠考试single_response | 是 | 是 | 是 | 否 | 否 | Good/Again |
| 考试revised | 仅改答组 | 仅改答组 | 否 | 否 | 否 | 无 |
| 考试telemetry_incomplete | 仅uncertain组 | 仅uncertain组 | 否 | 否 | 否 | 无 |
| omitted/abandoned | 单独计数 | 否 | 否 | 否 | 否 | 无 |
| disputed/invalidated | 隔离/失效组 | 主统计排除 | 否 | 否 | 否 | 无 |

普通证据是normal、early_review、可靠考试single_response的并集，每个attempt只计一次；purpose/source/提示条件作为独立维度仍可筛选，不把总数再与子组相加。待巩固只表示最近一次该并集观测错误：其中正确清除、错误设置；纠错正确不清除。提前正确清除该标志不代表跨日或无提示掌握。

冷却仅由cutoff之后其他不同目标lexeme的有效训练提交推进，不由考试选择/交卷推进；early_review明确计入。质量修订重建时重算证据及间隔资格，但保留所有曝光和历史会话位置。RT缺失不改变选择时间线资格；考试自动交卷的telemetry_incomplete优先于single_response，质量失效优先于其他组。

验收OB-02：A之后B–F五个early_review提交释放A普通间隔；同一B重复仅算一个不同lexeme；纠错不推进普通间隔；提前题占普通探索位置；early正确与correction正确对待巩固效果不同；各组与并集分母可对账。

## 6. 考试样例与版本迁移（R06）

原[content-v1](../fixtures/content-v1.json)保持不变。新增[exam-assembly-v1](../fixtures/exam-assembly-v1.json)登记独立原创来源，添加不明文提及river的bank语境题；选项仍明确引用原标准sense及语义身份。core种子合并两套内容，exam-conflict种子只使用原内容。

新增样例包括固定的合法三题型卷、原内容的无解组合、目标词重复与英文干扰项提示负例。契约检查必须依据真实fixture关系穷举小集合，验证正例可行、负例确实违反指定规则；这些检查不冒充尚未实现的HTTP组卷器E2E。

1.1.0采用observation-v2、selection-v2、budget-v2，grading-v1和FSRS权重不变。policy-v1保留为历史基线。新数据库默认v2；已有v1事实不得改写，必须在无活动会话时按v2维护重建后切换，新实例记录v2，旧实例和原报告保持原快照。没有版本信息的数据库拒绝自动猜测。HTTP仍为/api/v1，但契约版本升为1.1.0，旧客户端与新服务不混跑；GET /health返回contract_version供启动核对。
