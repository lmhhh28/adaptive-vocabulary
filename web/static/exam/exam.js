// exam.js: Pure Native ES Module for Exam Flow conforming to 10 R01

let csrfToken = null;

async function getCSRFToken() {
    if (csrfToken) return csrfToken;
    try {
        const res = await fetch('/client-context', {
            method: 'GET',
            headers: { 'Accept': 'application/json' },
            cache: 'no-store'
        });
        if (res.ok) {
            const data = await res.json();
            csrfToken = data.csrf_token;
            return csrfToken;
        }
    } catch (e) {
        console.error('Failed to get CSRF token', e);
    }
    return '';
}

function generateUUID() {
    if (typeof crypto !== 'undefined' && crypto.randomUUID) {
        return crypto.randomUUID();
    }
    return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, function(c) {
        const r = Math.random() * 16 | 0, v = c === 'x' ? r : (r & 0x3 | 0x8);
        return v.toString(16);
    });
}

// -----------------------------------------------------------
// 10 R01 Persistent Client Event Queue & Manager
// -----------------------------------------------------------
class ExamEventManager {
    constructor(examID) {
        this.examID = examID;
        this.storageKey = `avocab_exam_queue_${examID}`;
        this.clientSequence = 0;
        this.expectedRevision = 0;
        this.isProcessing = false;
        this.loadQueue();
    }

    loadQueue() {
        try {
            const raw = localStorage.getItem(this.storageKey);
            this.queue = raw ? JSON.parse(raw) : [];
        } catch (e) {
            this.queue = [];
        }
    }

    saveQueue() {
        try {
            localStorage.setItem(this.storageKey, JSON.stringify(this.queue));
        } catch (e) {
            console.error('Failed to persist queue to localStorage', e);
        }
    }

    initSync(serverRevision, lastAckSequence) {
        this.expectedRevision = serverRevision;
        this.clientSequence = Math.max(this.clientSequence, lastAckSequence);
    }

    async enqueueAction(instanceID, action, optionID) {
        this.clientSequence++;
        const eventItem = {
            client_event_id: generateUUID(),
            client_sequence: this.clientSequence,
            instance_id: instanceID,
            action: action,
            option_id: optionID,
            expected_revision: this.expectedRevision,
            rebase_count: 0,
            status: 'pending' // pending, unknown, rejected, acknowledged
        };

        this.queue.push(eventItem);
        this.saveQueue();
        this.processQueue();
        return eventItem;
    }

    async processQueue() {
        if (this.isProcessing || this.queue.length === 0) return;
        this.isProcessing = true;

        try {
            while (this.queue.length > 0) {
                const item = this.queue[0];
                const token = await getCSRFToken();

                const payload = {
                    instance_id: item.instance_id,
                    action: item.action,
                    client_event_id: item.client_event_id,
                    client_sequence: item.client_sequence,
                    expected_revision: item.expected_revision
                };
                if (item.action === 'select' && item.option_id) {
                    payload.option_id = item.option_id;
                }
                if (item.rebased_from) {
                    payload.rebased_from = item.rebased_from;
                }

                let response;
                try {
                    response = await fetch(`/api/v1/exams/${this.examID}/response-events`, {
                        method: 'POST',
                        headers: {
                            'Content-Type': 'application/json',
                            'X-CSRF-Token': token,
                            'Idempotency-Key': item.client_event_id
                        },
                        body: JSON.stringify(payload),
                        cache: 'no-store'
                    });
                } catch (netErr) {
                    // Network interrupted/unknown -> retry with same id/sequence/revision/body
                    item.status = 'unknown';
                    this.saveQueue();
                    console.warn('Network error, retrying event later:', netErr);
                    break;
                }

                if (response.ok) {
                    const data = await response.json();
                    this.expectedRevision = data.current_revision;
                    // Successfully accepted -> remove from queue
                    this.queue.shift();
                    this.saveQueue();
                    continue;
                }

                // Error handling per 10 R01
                let errBody = null;
                try {
                    errBody = await response.json();
                } catch (e) {}

                const errDetails = errBody?.error?.details || {};
                const errCode = errBody?.error?.code || '';

                if (response.status === 409 && errDetails.acceptance === 'not_accepted') {
                    // Explicit rejection! Can rebase
                    if (item.rebase_count < 3) {
                        item.rebase_count++;
                        item.rebased_from = {
                            client_sequence: item.client_sequence,
                            expected_revision: item.expected_revision
                        };
                        this.expectedRevision = errDetails.current_revision ?? (this.expectedRevision + 1);
                        this.clientSequence++;
                        item.client_sequence = this.clientSequence;
                        item.expected_revision = this.expectedRevision;
                        this.saveQueue();
                        continue;
                    } else {
                        // Max retries reached
                        item.status = 'rejected';
                        this.saveQueue();
                        alert('连续多次版本冲突，请检查网络并刷新页面');
                        break;
                    }
                } else if (errCode === 'EXAM_SUBMITTED' || errCode === 'SESSION_CLOSED') {
                    this.queue = [];
                    this.saveQueue();
                    window.location.href = `/exams/${this.examID}/result`;
                    break;
                } else {
                    // Unknown or unexpected error -> leave as unknown and pause
                    item.status = 'unknown';
                    this.saveQueue();
                    break;
                }
            }
        } finally {
            this.isProcessing = false;
        }
    }

    async triggerTelemetryGap() {
        const token = await getCSRFToken();
        const key = generateUUID();
        const res = await fetch(`/api/v1/exams/${this.examID}/telemetry-gaps`, {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
                'X-CSRF-Token': token,
                'Idempotency-Key': key
            },
            body: JSON.stringify({
                expected_revision: this.expectedRevision,
                reason: 'lost_queue'
            }),
            cache: 'no-store'
        });
        if (res.ok) {
            const data = await res.json();
            this.expectedRevision = data.current_revision;
            return true;
        }
        return false;
    }
}

// -----------------------------------------------------------
// Main UI Controller
// -----------------------------------------------------------
document.addEventListener('DOMContentLoaded', async () => {
    const appEl = document.getElementById('exam-app');
    const resultAppEl = document.getElementById('exam-result-app');

    if (resultAppEl) {
        initResultView(resultAppEl.dataset.examId);
        return;
    }

    if (!appEl) return;
    const mode = appEl.dataset.mode;
    const examID = appEl.dataset.examId;

    if (mode === 'new') {
        initNewExamView();
    } else if (mode === 'take' && examID) {
        initTakeExamView(examID);
    }
});

// View 1: New Exam Creation
function initNewExamView() {
    const form = document.getElementById('create-exam-form');
    if (!form) return;

    form.addEventListener('submit', async (e) => {
        e.preventDefault();
        const bankID = document.getElementById('bank-select').value;
        const count = parseInt(document.getElementById('question-count').value, 10);
        const timeLimitVal = document.getElementById('time-limit').value;
        const timeLimitSeconds = timeLimitVal ? parseInt(timeLimitVal, 10) * 60 : null;

        const btn = document.getElementById('btn-create-exam');
        btn.disabled = true;
        btn.textContent = '正在组卷中...';

        const token = await getCSRFToken();
        const idempKey = generateUUID();

        const payload = {
            bank_id: bankID,
            question_count: count
        };
        if (timeLimitSeconds) {
            payload.time_limit_seconds = timeLimitSeconds;
        }

        try {
            const res = await fetch('/api/v1/exams', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                    'X-CSRF-Token': token,
                    'Idempotency-Key': idempKey
                },
                body: JSON.stringify(payload),
                cache: 'no-store'
            });

            if (res.ok) {
                const data = await res.json();
                window.location.href = `/exams/${data.exam_id}`;
            } else {
                const err = await res.json().catch(() => ({}));
                alert(`组卷失败：${err.error?.message || res.statusText}`);
                btn.disabled = false;
                btn.textContent = '开始组卷考试';
            }
        } catch (err) {
            alert('网络请求失败：' + err.message);
            btn.disabled = false;
            btn.textContent = '开始组卷考试';
        }
    });
}

// View 2: Take Exam
async function initTakeExamView(examID) {
    const eventManager = new ExamEventManager(examID);

    let examData = null;
    let currentIndex = 0;
    let draftsMap = {}; // instance_id -> selected_option_id

    // Load initial exam details
    try {
        const res = await fetch(`/api/v1/exams/${examID}`, {
            headers: { 'Accept': 'application/json' },
            cache: 'no-store'
        });
        if (!res.ok) {
            alert('无法加载考试详情');
            return;
        }
        examData = await res.json();
    } catch (e) {
        alert('网络加载失败：' + e.message);
        return;
    }

    if (examData.status === 'submitted') {
        window.location.href = `/exams/${examID}/result`;
        return;
    }

    eventManager.initSync(examData.session_revision, examData.last_ack_sequence);

    // Populate initial drafts
    (examData.drafts || []).forEach(d => {
        draftsMap[d.instance_id] = d.selected_option_id;
    });

    const questions = examData.public_questions || [];

    // Timer setup
    if (examData.deadline) {
        const deadlineMs = new Date(examData.deadline).getTime();
        const timerEl = document.getElementById('exam-timer');
        let timerInterval;
        const updateTimer = () => {
            const now = Date.now();
            const remainSec = Math.max(0, Math.floor((deadlineMs - now) / 1000));
            const mins = Math.floor(remainSec / 60);
            const secs = remainSec % 60;
            if (timerEl) {
                timerEl.textContent = `${mins.toString().padStart(2, '0')}:${secs.toString().padStart(2, '0')}`;
            }
            if (remainSec <= 0) {
                clearInterval(timerInterval);
                alert('考试时间已到，系统正在自动交卷...');
                submitExamAction();
            }
        };
        updateTimer();
        timerInterval = setInterval(updateTimer, 1000);
    } else {
        const timerWrap = document.getElementById('timer-wrap');
        if (timerWrap) timerWrap.style.display = 'none';
    }

    function renderNavSheet() {
        const navGrid = document.getElementById('exam-nav-grid');
        if (!navGrid) return;
        navGrid.innerHTML = '';

        questions.forEach((q, idx) => {
            const bubble = document.createElement('div');
            bubble.className = 'nav-bubble';
            bubble.textContent = idx + 1;
            if (idx === currentIndex) bubble.classList.add('active');
            if (draftsMap[q.instance_id]) bubble.classList.add('answered');

            bubble.addEventListener('click', () => {
                currentIndex = idx;
                renderCurrentQuestion();
            });
            navGrid.appendChild(bubble);
        });
    }

    function renderCurrentQuestion() {
        if (questions.length === 0) return;
        const q = questions[currentIndex];

        document.getElementById('q-ordinal-label').textContent = `题目 ${currentIndex + 1} / ${questions.length}`;
        document.getElementById('exam-progress-label').textContent = `第 ${currentIndex + 1} / ${questions.length} 题`;
        document.getElementById('q-task-family-label').textContent = q.task_family;
        document.getElementById('q-stem').textContent = q.stem;

        const optionsList = document.getElementById('q-options-list');
        optionsList.innerHTML = '';

        const letters = ['A', 'B', 'C', 'D'];
        q.options.forEach((opt, idx) => {
            const item = document.createElement('div');
            item.className = 'option-item';
            if (draftsMap[q.instance_id] === opt.id) {
                item.classList.add('selected');
            }

            item.innerHTML = `
                <div class="option-prefix">${letters[idx] || ''}</div>
                <div class="option-text"></div>
            `;
            item.querySelector('.option-text').textContent = opt.text;

            item.addEventListener('click', () => {
                if (draftsMap[q.instance_id] === opt.id) return; // already selected
                draftsMap[q.instance_id] = opt.id;
                renderCurrentQuestion();
                eventManager.enqueueAction(q.instance_id, 'select', opt.id);
            });

            optionsList.appendChild(item);
        });

        // Prev/Next button states
        document.getElementById('btn-prev-q').disabled = (currentIndex === 0);
        document.getElementById('btn-next-q').disabled = (currentIndex === questions.length - 1);

        renderNavSheet();
    }

    document.getElementById('btn-prev-q')?.addEventListener('click', () => {
        if (currentIndex > 0) {
            currentIndex--;
            renderCurrentQuestion();
        }
    });

    document.getElementById('btn-next-q')?.addEventListener('click', () => {
        if (currentIndex < questions.length - 1) {
            currentIndex++;
            renderCurrentQuestion();
        }
    });

    document.getElementById('btn-clear-selection')?.addEventListener('click', () => {
        const q = questions[currentIndex];
        if (!draftsMap[q.instance_id]) return;
        draftsMap[q.instance_id] = null;
        renderCurrentQuestion();
        eventManager.enqueueAction(q.instance_id, 'clear', null);
    });

    // Keyboard navigation in Exam
    document.addEventListener('keydown', (e) => {
        if (['INPUT', 'TEXTAREA', 'SELECT'].includes(document.activeElement?.tagName)) return;
        const q = questions[currentIndex];
        if (!q) return;

        const keyMap = { '1': 0, '2': 1, '3': 2, '4': 3, 'a': 0, 'b': 1, 'c': 2, 'd': 3, 'A': 0, 'B': 1, 'C': 2, 'D': 3 };
        if (e.key in keyMap && q.options) {
            const idx = keyMap[e.key];
            if (idx < q.options.length) {
                const opt = q.options[idx];
                if (draftsMap[q.instance_id] !== opt.id) {
                    draftsMap[q.instance_id] = opt.id;
                    renderCurrentQuestion();
                    eventManager.enqueueAction(q.instance_id, 'select', opt.id);
                }
            }
        } else if (e.key === 'ArrowLeft') {
            if (currentIndex > 0) {
                currentIndex--;
                renderCurrentQuestion();
            }
        } else if (e.key === 'ArrowRight') {
            if (currentIndex < questions.length - 1) {
                currentIndex++;
                renderCurrentQuestion();
            }
        }
    });

    async function submitExamAction() {
        const btn = document.getElementById('btn-submit-exam');
        if (btn) btn.disabled = true;

        // Ensure all pending events are sent
        await eventManager.processQueue();

        const token = await getCSRFToken();
        const submissionID = generateUUID();

        try {
            const res = await fetch(`/api/v1/exams/${examID}/submit`, {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                    'X-CSRF-Token': token,
                    'Idempotency-Key': submissionID
                },
                body: JSON.stringify({
                    expected_revision: eventManager.expectedRevision,
                    submission_id: submissionID,
                    last_ack_sequence: eventManager.clientSequence
                }),
                cache: 'no-store'
            });

            if (res.ok) {
                const data = await res.json();
                window.location.href = data.result_url || `/exams/${examID}/result`;
            } else {
                const err = await res.json().catch(() => ({}));
                alert(`交卷失败：${err.error?.message || res.statusText}`);
                if (btn) btn.disabled = false;
            }
        } catch (e) {
            alert('提交失败：' + e.message);
            if (btn) btn.disabled = false;
        }
    }

    document.getElementById('btn-submit-exam')?.addEventListener('click', () => {
        const unanswered = questions.filter(q => !draftsMap[q.instance_id]).length;
        let msg = '确定现在提交试卷吗？';
        if (unanswered > 0) {
            msg = `您还有 ${unanswered} 道题未作答，确定现在提交试卷吗？`;
        }
        if (confirm(msg)) {
            submitExamAction();
        }
    });

    document.getElementById('btn-abandon-exam')?.addEventListener('click', async () => {
        if (!confirm('确定放弃本次考试吗？已答题目将不计入评分。')) return;
        const token = await getCSRFToken();
        try {
            const res = await fetch(`/api/v1/exams/${examID}/abandon`, {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                    'X-CSRF-Token': token
                },
                body: JSON.stringify({
                    expected_revision: eventManager.expectedRevision
                }),
                cache: 'no-store'
            });
            if (res.ok) {
                window.location.href = '/exams/new';
            }
        } catch (e) {
            alert('操作失败：' + e.message);
        }
    });

    renderCurrentQuestion();
}

// View 3: Exam Result Report
async function initResultView(examID) {
    let result = null;
    try {
        const res = await fetch(`/api/v1/exams/${examID}/result`, {
            headers: { 'Accept': 'application/json' },
            cache: 'no-store'
        });
        if (!res.ok) {
            alert('无法获取评测报告');
            return;
        }
        result = await res.json();
    } catch (e) {
        alert('网络加载失败：' + e.message);
        return;
    }

    // Populate Overview
    const ratioStr = (result.ratio !== null && result.ratio !== undefined)
        ? Math.round(result.ratio * 100) + '%'
        : '0%';
    document.getElementById('stat-ratio').textContent = ratioStr;
    document.getElementById('stat-correct').textContent = result.correct;
    document.getElementById('stat-total').textContent = result.total;
    document.getElementById('stat-omitted').textContent = result.omitted;

    // Behavior Analysis
    const analysis = result.response_analysis || {};
    const firstAcc = analysis.reliable_first_choices > 0
        ? Math.round((analysis.first_choice_correct_count / analysis.reliable_first_choices) * 100) + '%'
        : '无可靠样本';
    document.getElementById('first-choice-accuracy').textContent = firstAcc;
    document.getElementById('revised-to-correct').textContent = `${analysis.revised_to_correct_count || 0} 次`;
    document.getElementById('revised-to-incorrect').textContent = `${analysis.revised_to_incorrect_count || 0} 次`;

    // Questions Review List
    const listEl = document.getElementById('questions-review-list');
    listEl.innerHTML = '';

    (result.questions || []).forEach((q, idx) => {
        const card = document.createElement('div');
        let statusClass = 'incorrect';
        let statusText = '回答错误';
        if (q.is_correct) {
            statusClass = 'correct';
            statusText = '回答正确';
        } else if (!q.selected_option_id) {
            statusClass = 'omitted';
            statusText = '未作答';
        }

        card.className = `result-question-item ${statusClass}`;

        const headerDiv = document.createElement('div');
        headerDiv.className = 'result-q-header';
        
        const boldSpan = document.createElement('span');
        boldSpan.className = 'font-bold';
        boldSpan.textContent = `第 ${idx + 1} 题 (${q.task_family})`;
        
        const badgeSpan = document.createElement('span');
        badgeSpan.className = `badge ${q.is_correct ? 'badge-primary' : 'badge-secondary'}`;
        badgeSpan.textContent = statusText;
        
        headerDiv.appendChild(boldSpan);
        headerDiv.appendChild(badgeSpan);
        
        const stemDiv = document.createElement('div');
        stemDiv.style.cssText = 'font-size: 1.1rem; margin-bottom: 0.75rem;';
        stemDiv.textContent = q.stem;
        
        const optionsDiv = document.createElement('div');
        optionsDiv.className = 'result-options';
        optionsDiv.style.marginLeft = '0.5rem';
        
        const letters = ['A', 'B', 'C', 'D'];
        q.options.forEach((opt, oIdx) => {
            const optDiv = document.createElement('div');
            optDiv.style.margin = '0.35rem 0';
            optDiv.textContent = `${letters[oIdx]}. ${opt.text}`;
            
            if (opt.id === q.correct_option_id) {
                const tag1 = document.createElement('strong');
                tag1.style.color = '#10b981';
                tag1.textContent = ' [正确答案]';
                optDiv.appendChild(tag1);
            }
            if (opt.id === q.selected_option_id) {
                const tag2 = document.createElement('span');
                tag2.style.color = '#3b82f6';
                tag2.textContent = ' [您的选择]';
                optDiv.appendChild(tag2);
            }
            optionsDiv.appendChild(optDiv);
        });

        const explanationDiv = document.createElement('div');
        explanationDiv.className = 'result-explanation';
        
        const explStrong = document.createElement('strong');
        explStrong.textContent = '解析：';
        explanationDiv.appendChild(explStrong);
        
        explanationDiv.appendChild(document.createTextNode(q.explanation || '暂无详细解析'));
        
        card.appendChild(headerDiv);
        card.appendChild(stemDiv);
        card.appendChild(optionsDiv);
        card.appendChild(explanationDiv);

        listEl.appendChild(card);
    });
}
