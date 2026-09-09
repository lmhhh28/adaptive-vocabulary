// Practice Application Logic

function generateUUID() {
    if (crypto.randomUUID) {
        return crypto.randomUUID();
    }
    return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, function(c) {
        const r = Math.random() * 16 | 0;
        const v = c === 'x' ? r : (r & 0x3 | 0x8);
        return v.toString(16);
    });
}

let cachedCSRF = null;

async function getCSRFToken() {
    if (cachedCSRF) return cachedCSRF;
    try {
        const res = await fetch('/client-context', {
            method: 'GET',
            headers: { 'Accept': 'application/json' }
        });
        if (res.ok) {
            const data = await res.json();
            cachedCSRF = data.csrf_token;
            return cachedCSRF;
        }
    } catch (e) {
        console.error('Failed to fetch client context', e);
    }
    return '';
}

class PracticeApp {
    constructor() {
        this.appEl = document.getElementById('practice-app');
        this.statusBanner = document.getElementById('status-banner');
        
        // Views
        this.bankSelectView = document.getElementById('bank-select-view');
        this.practiceView = document.getElementById('practice-view');
        this.unavailableView = document.getElementById('unavailable-view');
        this.summaryView = document.getElementById('summary-view');

        // Practice View Elements
        this.taskFamilyBadge = document.getElementById('task-family-badge');
        this.questionIndexLabel = document.getElementById('question-index-label');
        this.questionStem = document.getElementById('question-stem');
        this.optionsList = document.getElementById('options-list');
        this.submitRow = document.getElementById('submit-row');
        this.btnSubmit = document.getElementById('btn-submit');
        this.btnEndPractice = document.getElementById('btn-end-practice');

        // Feedback Elements
        this.feedbackSection = document.getElementById('feedback-section');
        this.feedbackBanner = document.getElementById('feedback-result-banner');
        this.correctAnswerVal = document.getElementById('correct-answer-val');
        this.explanationVal = document.getElementById('explanation-val');
        this.btnNext = document.getElementById('btn-next');

        // Unavailable Elements
        this.unavailableReason = document.getElementById('unavailable-reason');
        this.unavailableCounts = document.getElementById('unavailable-counts');
        this.btnEndUnavailable = document.getElementById('btn-end-unavailable');

        // Summary Elements
        this.summaryAnsweredCount = document.getElementById('summary-answered-count');
        this.summaryUnresolvedCount = document.getElementById('summary-unresolved-count');
        this.btnRestartPractice = document.getElementById('btn-restart-practice');

        // State
        this.currentSession = null;
        this.currentQuestion = null;
        this.selectedOptionID = null;
        this.isSubmitted = false;
        this.answeredCount = 0;

        this.init();
    }

    showError(msg) {
        this.statusBanner.textContent = msg;
        this.statusBanner.className = 'alert-banner error';
        this.statusBanner.classList.remove('hidden');
    }

    hideError() {
        this.statusBanner.classList.add('hidden');
    }

    showView(view) {
        this.bankSelectView.classList.add('hidden');
        this.practiceView.classList.add('hidden');
        this.unavailableView.classList.add('hidden');
        this.summaryView.classList.add('hidden');

        if (view) {
            view.classList.remove('hidden');
        }
    }

    async init() {
        await getCSRFToken();

        // Bind event listeners
        this.bindEvents();

        // Check active session
        try {
            const res = await fetch('/api/v1/sessions/active', { credentials: 'same-origin' });
            if (res.ok) {
                const data = await res.json();
                if (data.session && data.session.kind === 'practice') {
                    await this.resumeSession(data.session);
                    return;
                }
            }
        } catch (e) {
            console.error('Failed to check active session', e);
        }

        // Check initial bank ID in URL or data attribute
        const initialBankID = this.appEl.getAttribute('data-initial-bank-id');
        if (initialBankID) {
            await this.startPractice(initialBankID);
            return;
        }

        this.showView(this.bankSelectView);
    }

    bindEvents() {
        // Bank selection buttons
        document.querySelectorAll('.btn-start-bank').forEach(btn => {
            btn.addEventListener('click', () => {
                const bankID = btn.getAttribute('data-bank-id');
                this.startPractice(bankID);
            });
        });

        // Submit answer
        this.btnSubmit.addEventListener('click', () => this.submitAnswer());

        // Next question
        this.btnNext.addEventListener('click', () => this.loadNextQuestion());

        // End practice
        this.btnEndPractice.addEventListener('click', () => {
            if (confirm('确定要结束当前练习吗？未完成的纠错将保留至后续练习。')) {
                this.endPractice('ended');
            }
        });

        // End unavailable
        this.btnEndUnavailable.addEventListener('click', () => {
            this.endPractice('ended');
        });

        // Restart practice
        this.btnRestartPractice.addEventListener('click', () => {
            this.showView(this.bankSelectView);
        });

        // Keyboard navigation
        document.addEventListener('keydown', (e) => {
            if (this.practiceView.classList.contains('hidden')) return;

            if (!this.isSubmitted) {
                // Key 1-4 or A-D
                const keyMap = { '1': 0, '2': 1, '3': 2, '4': 3, 'a': 0, 'b': 1, 'c': 2, 'd': 3, 'A': 0, 'B': 1, 'C': 2, 'D': 3 };
                if (e.key in keyMap && this.currentQuestion && this.currentQuestion.options) {
                    const idx = keyMap[e.key];
                    if (idx < this.currentQuestion.options.length) {
                        this.selectOption(this.currentQuestion.options[idx].id);
                    }
                } else if (e.key === 'Enter' && this.selectedOptionID) {
                    this.submitAnswer();
                }
            } else {
                // After submission: Enter advances to next
                if (e.key === 'Enter') {
                    this.loadNextQuestion();
                }
            }
        });
    }

    async startPractice(bankID) {
        this.hideError();
        const csrf = await getCSRFToken();
        const idempotencyKey = generateUUID();

        try {
            const res = await fetch('/api/v1/practice-sessions', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                    'X-CSRF-Token': csrf,
                    'Idempotency-Key': idempotencyKey,
                },
                body: JSON.stringify({ bank_id: bankID }),
                credentials: 'same-origin',
            });

            if (!res.ok) {
                const errData = await res.json().catch(() => ({}));
                this.showError(errData.error?.message || '开启练习失败');
                return;
            }

            const data = await res.json();
            this.currentSession = data.session;
            await this.loadNextQuestion();
        } catch (e) {
            this.showError('网络连接失败，请重试');
        }
    }

    async resumeSession(session) {
        this.currentSession = session;
        try {
            const res = await fetch(`/api/v1/practice-sessions/${session.id}`, { credentials: 'same-origin' });
            if (res.ok) {
                const data = await res.json();
                this.currentSession = data.session;
                if (data.current_question) {
                    this.renderQuestion(data.current_question);
                    return;
                }
            }
        } catch (e) {
            console.error('Failed to load session details', e);
        }
        await this.loadNextQuestion();
    }

    async loadNextQuestion() {
        this.hideError();
        this.isSubmitted = false;
        this.selectedOptionID = null;

        const csrf = await getCSRFToken();
        try {
            const res = await fetch(`/api/v1/practice-sessions/${this.currentSession.id}/next`, {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                    'X-CSRF-Token': csrf,
                },
                body: JSON.stringify({ expected_cursor: this.currentSession.cursor }),
                credentials: 'same-origin',
            });

            if (!res.ok) {
                const errData = await res.json().catch(() => ({}));
                if (errData.error?.code === 'REVISION_CONFLICT') {
                    // Re-sync cursor
                    if (errData.error.details && typeof errData.error.details.cursor === 'number') {
                        this.currentSession.cursor = errData.error.details.cursor;
                        return this.loadNextQuestion();
                    }
                }
                this.showError(errData.error?.message || '获取题目失败');
                return;
            }

            const data = await res.json();
            if (data.status === 'unavailable') {
                this.renderUnavailable(data);
            } else if (data.status === 'ready' && data.question) {
                this.currentSession.cursor = data.cursor;
                this.currentSession.revision = data.session_revision;
                this.renderQuestion(data.question);
            }
        } catch (e) {
            this.showError('网络连接失败，无法加载题目');
        }
    }

    renderQuestion(question) {
        this.currentQuestion = question;
        this.isSubmitted = false;
        this.selectedOptionID = null;

        // Update task family label
        const taskFamilyNames = {
            'meaning_recognition': '词义识别',
            'reverse_selection': '反向选择',
            'context_selection': '语境选择',
        };
        this.taskFamilyBadge.textContent = taskFamilyNames[question.task_family] || '词汇练习';
        this.questionIndexLabel.textContent = `第 ${question.ordinal + 1} 题`;

        // Render stem
        this.questionStem.textContent = question.stem;

        // Render options
        this.optionsList.innerHTML = '';
        const letters = ['A', 'B', 'C', 'D', 'E', 'F'];
        question.options.forEach((opt, idx) => {
            const btn = document.createElement('div');
            btn.className = 'option-btn';
            btn.setAttribute('data-option-id', opt.id);

            const badge = document.createElement('span');
            badge.className = 'option-badge';
            badge.textContent = letters[idx] || (idx + 1);

            const text = document.createElement('span');
            text.className = 'option-text';
            text.textContent = opt.text;

            btn.appendChild(badge);
            btn.appendChild(text);

            btn.addEventListener('click', () => {
                if (!this.isSubmitted) {
                    this.selectOption(opt.id);
                }
            });

            this.optionsList.appendChild(btn);
        });

        // Reset submit and feedback buttons
        this.btnSubmit.disabled = true;
        this.submitRow.classList.remove('hidden');
        this.feedbackSection.classList.add('hidden');

        this.showView(this.practiceView);
    }

    selectOption(optionID) {
        this.selectedOptionID = optionID;
        document.querySelectorAll('.option-btn').forEach(btn => {
            if (btn.getAttribute('data-option-id') === optionID) {
                btn.classList.add('selected');
            } else {
                btn.classList.remove('selected');
            }
        });
        this.btnSubmit.disabled = false;
    }

    async submitAnswer() {
        if (!this.selectedOptionID || this.isSubmitted) return;

        this.isSubmitted = true;
        this.btnSubmit.disabled = true;
        this.btnSubmit.textContent = '提交中...';

        const csrf = await getCSRFToken();
        const idempotencyKey = generateUUID();
        const submissionID = generateUUID();

        try {
            const res = await fetch(`/api/v1/practice-sessions/${this.currentSession.id}/attempts`, {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                    'X-CSRF-Token': csrf,
                    'Idempotency-Key': idempotencyKey,
                },
                body: JSON.stringify({
                    instance_id: this.currentQuestion.instance_id,
                    option_id: this.selectedOptionID,
                    submission_id: submissionID,
                }),
                credentials: 'same-origin',
            });

            if (!res.ok) {
                const errData = await res.json().catch(() => ({}));
                this.showError(errData.error?.message || '提交答案失败');
                this.btnSubmit.disabled = false;
                this.btnSubmit.textContent = '提交答案';
                this.isSubmitted = false;
                return;
            }

            const feedback = await res.json();
            this.answeredCount++;
            this.currentSession.cursor = feedback.next_cursor;
            this.currentSession.revision = feedback.session_revision;

            this.renderFeedback(feedback);
        } catch (e) {
            this.showError('网络连接失败，请重试');
            this.btnSubmit.disabled = false;
            this.btnSubmit.textContent = '提交答案';
            this.isSubmitted = false;
        }
    }

    renderFeedback(feedback) {
        this.submitRow.classList.add('hidden');

        // Mark options
        let correctText = '';
        document.querySelectorAll('.option-btn').forEach(btn => {
            btn.classList.add('disabled');
            const optID = btn.getAttribute('data-option-id');
            if (optID === feedback.correct_option_id) {
                btn.classList.add('correct');
                correctText = btn.querySelector('.option-text').textContent;
            } else if (optID === this.selectedOptionID && !feedback.correct) {
                btn.classList.add('wrong');
            }
        });

        if (!correctText && this.currentQuestion && this.currentQuestion.options) {
            const found = this.currentQuestion.options.find(o => o.id === feedback.correct_option_id);
            if (found) {
                correctText = found.text;
            }
        }

        // Banner
        if (feedback.correct) {
            this.feedbackBanner.className = 'feedback-banner banner-correct';
            this.feedbackBanner.textContent = '✓ 回答正确！';
        } else {
            this.feedbackBanner.className = 'feedback-banner banner-wrong';
            this.feedbackBanner.textContent = '✗ 回答错误！';
        }

        // Details
        this.correctAnswerVal.textContent = correctText;
        this.explanationVal.textContent = feedback.explanation || '暂无详细解析';

        this.feedbackSection.classList.remove('hidden');
        this.btnNext.focus();
    }

    renderUnavailable(data) {
        let msg = '当前题库中的所有题目均处于冷却中或已完成本轮学习。';
        if (data.counts) {
            this.unavailableCounts.textContent = `冷却中: ${data.counts.cooldown || 0} | 缺少题目: ${data.counts.content_missing || 0} | 已用尽: ${data.counts.exhausted || 0}`;
        }
        this.unavailableReason.textContent = msg;
        this.showView(this.unavailableView);
    }

    async endPractice(reason) {
        const csrf = await getCSRFToken();
        try {
            const res = await fetch(`/api/v1/practice-sessions/${this.currentSession.id}/end`, {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                    'X-CSRF-Token': csrf,
                },
                body: JSON.stringify({
                    expected_revision: this.currentSession.revision,
                    reason: reason || 'ended',
                }),
                credentials: 'same-origin',
            });

            if (res.ok) {
                const data = await res.json();
                this.summaryAnsweredCount.textContent = this.answeredCount;
                this.summaryUnresolvedCount.textContent = data.unresolved_unit_count || 0;
                this.currentSession = null;
                this.showView(this.summaryView);
            } else {
                const errData = await res.json().catch(() => ({}));
                this.showError(errData.error?.message || '结束练习失败');
            }
        } catch (e) {
            this.showError('网络连接失败');
        }
    }
}

document.addEventListener('DOMContentLoaded', () => {
    new PracticeApp();
});
