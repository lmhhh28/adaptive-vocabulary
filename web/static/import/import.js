// Import and Disambiguation Client Logic

let csrfToken = null;

async function initClientContext() {
    try {
        const resp = await fetch('/client-context', {
            headers: { 'Cache-Control': 'no-store' }
        });
        if (resp.ok) {
            const data = await resp.json();
            csrfToken = data.csrf_token;
        }
    } catch (e) {
        console.error('Failed to initialize client context:', e);
    }
}

function showBanner(msg, isError = false) {
    const banner = document.getElementById('status-banner');
    if (!banner) return;
    banner.textContent = msg;
    banner.className = 'alert-banner ' + (isError ? 'alert-danger' : 'alert-success');
    banner.classList.remove('hidden');
}

// ----------------- Page 1: Import Upload -----------------
function setupUploadPage() {
    const form = document.getElementById('import-form');
    const uploadCard = document.getElementById('import-upload-card');
    if (!form || !uploadCard) return;

    const bankID = uploadCard.dataset.bankId;

    form.addEventListener('submit', async (e) => {
        e.preventDefault();
        const submitBtn = document.getElementById('btn-submit-import');
        submitBtn.disabled = true;
        submitBtn.textContent = '正在解析导入...';

        try {
            const fileInput = document.getElementById('import-file');
            const textInput = document.getElementById('import-text');

            let resp;
            if (fileInput.files && fileInput.files.length > 0) {
                // Multipart upload
                const formData = new FormData();
                formData.append('file', fileInput.files[0]);
                formData.append('resolution_policy_version', 'v1');

                resp = await fetch(`/api/v1/banks/${bankID}/imports`, {
                    method: 'POST',
                    headers: {
                        'X-CSRF-Token': csrfToken || '',
                    },
                    body: formData,
                });
            } else if (textInput.value.trim().length > 0) {
                // JSON text upload
                resp = await fetch(`/api/v1/banks/${bankID}/imports`, {
                    method: 'POST',
                    headers: {
                        'Content-Type': 'application/json',
                        'X-CSRF-Token': csrfToken || '',
                    },
                    body: JSON.stringify({
                        text: textInput.value.trim(),
                        resolution_policy_version: 'v1',
                    }),
                });
            } else {
                showBanner('请选择文件或粘贴单词列表', true);
                submitBtn.disabled = false;
                submitBtn.textContent = '开始导入解析';
                return;
            }

            if (!resp.ok) {
                const errData = await resp.json().catch(() => ({}));
                showBanner(errData.error?.message || '导入解析失败', true);
                submitBtn.disabled = false;
                submitBtn.textContent = '开始导入解析';
                return;
            }

            const data = await resp.json();
            // Redirect to detail page
            window.location.href = `/imports/${data.import_id}`;
        } catch (err) {
            showBanner('网络错误: ' + err.message, true);
            submitBtn.disabled = false;
            submitBtn.textContent = '开始导入解析';
        }
    });
}

// ----------------- Page 2: Import Detail & Disambiguation -----------------
let currentFilter = '';

function setupDetailPage() {
    const app = document.getElementById('import-detail-app');
    if (!app) return;

    const importID = app.dataset.importId;

    // Filter button bindings
    document.querySelectorAll('.filter-btn').forEach(btn => {
        btn.addEventListener('click', () => {
            document.querySelectorAll('.filter-btn').forEach(b => b.classList.remove('active'));
            btn.classList.add('active');
            currentFilter = btn.dataset.status;
            loadItems(importID, currentFilter);
        });
    });

    loadItems(importID, currentFilter);
}

async function loadItems(importID, statusFilter) {
    const container = document.getElementById('items-container');
    container.innerHTML = '<p class="loading-text">正在加载导入条目...</p>';

    let url = `/api/v1/imports/${importID}/items?limit=100`;
    if (statusFilter) {
        url += `&status=${encodeURIComponent(statusFilter)}`;
    }

    try {
        const resp = await fetch(url, { headers: { 'Cache-Control': 'no-store' } });
        if (!resp.ok) {
            container.innerHTML = '<p class="text-danger">加载条目失败</p>';
            return;
        }

        const data = await resp.json();
        updateStatsBar(data.counts, data.total);
        renderItems(data.items, importID);
    } catch (e) {
        container.innerHTML = '<p class="text-danger">网络异常: ' + e.message + '</p>';
    }
}

function updateStatsBar(counts, total) {
    if (!counts) return;
    document.getElementById('stat-total-val').textContent = total ?? '-';
    document.getElementById('stat-resolved-val').textContent = counts.resolved ?? 0;
    document.getElementById('stat-ambiguous-val').textContent = counts.ambiguous ?? 0;
    document.getElementById('stat-not-found-val').textContent = counts.not_found ?? 0;
    document.getElementById('stat-invalid-val').textContent = counts.invalid ?? 0;
    document.getElementById('stat-excluded-val').textContent = counts.excluded ?? 0;
}

function renderItems(items, importID) {
    const container = document.getElementById('items-container');
    if (!items || items.length === 0) {
        container.innerHTML = '<p class="empty-state">当前筛选条件下暂无条目</p>';
        return;
    }

    container.innerHTML = '';
    items.forEach(item => {
        const card = document.createElement('div');
        card.className = `item-card status-${item.status}`;
        card.id = `item-card-${item.id}`;

        let statusText = {
            'resolved': '已采纳',
            'ambiguous': '待消歧',
            'not_found': '未匹配',
            'invalid': '无效',
            'excluded': '已排除'
        }[item.status] || item.status;

        let candidatesHTML = '';
        if (item.candidates && item.candidates.length > 0) {
            candidatesHTML = `
                <div class="candidates-box">
                    <p class="text-muted" style="margin-bottom:0.4rem;font-size:0.85rem;">候选标准词义 (请选择采纳项):</p>
                    ${item.candidates.map((c, idx) => `
                        <label class="candidate-option">
                            <input type="radio" name="candidate-${item.id}" value="${c.sense_id}" ${idx === 0 ? 'checked' : ''} ${item.status !== 'ambiguous' ? 'disabled' : ''}>
                            <div>
                                <span class="candidate-pos">[${c.part_of_speech}]</span>
                                <span class="candidate-meaning">${c.meaning}</span>
                            </div>
                        </label>
                    `).join('')}
                </div>
            `;
        }

        let actionButtonsHTML = '';
        if (item.status === 'ambiguous') {
            actionButtonsHTML = `
                <div class="item-actions">
                    <button class="btn btn-primary btn-xs btn-adopt" data-item-id="${item.id}" data-rev="${item.revision}">采纳所选词义</button>
                    <button class="btn btn-outline btn-xs btn-exclude" data-item-id="${item.id}" data-rev="${item.revision}">排除此词</button>
                </div>
            `;
        } else if (item.status === 'not_found') {
            actionButtonsHTML = `
                <div class="item-actions">
                    <button class="btn btn-outline btn-xs btn-exclude" data-item-id="${item.id}" data-rev="${item.revision}">标记排除</button>
                </div>
            `;
        }

        card.innerHTML = `
            <div class="item-header">
                <div>
                    <span class="item-word">${item.normalized_word || item.raw_input}</span>
                    ${item.raw_input !== item.normalized_word ? `<small class="text-muted"> (原始输入: ${item.raw_input})</small>` : ''}
                </div>
                <span class="item-status-badge badge-${item.status}">${statusText}</span>
            </div>
            <div class="item-body">
                ${item.disposition_reason ? `<p class="meta-info">原因: ${item.disposition_reason}</p>` : ''}
                ${candidatesHTML}
                ${actionButtonsHTML}
            </div>
        `;

        container.appendChild(card);
    });

    // Bind item action events
    container.querySelectorAll('.btn-adopt').forEach(btn => {
        btn.addEventListener('click', async () => {
            const itemID = btn.dataset.itemId;
            const rev = parseInt(btn.dataset.rev, 10);
            const selectedRadio = document.querySelector(`input[name="candidate-${itemID}"]:checked`);
            if (!selectedRadio) {
                alert('请先选择一个词义');
                return;
            }
            btn.disabled = true;
            btn.textContent = '处理中...';

            await resolveItem(importID, itemID, 'adopt', [selectedRadio.value], '页面人工选择消歧采纳', rev);
        });
    });

    container.querySelectorAll('.btn-exclude').forEach(btn => {
        btn.addEventListener('click', async () => {
            const itemID = btn.dataset.itemId;
            const rev = parseInt(btn.dataset.rev, 10);
            btn.disabled = true;
            btn.textContent = '排除中...';

            await resolveItem(importID, itemID, 'exclude', [], '页面人工标记排除', rev);
        });
    });
}

async function resolveItem(importID, itemID, action, senseRefs, reason, expectedRev) {
    try {
        const resp = await fetch(`/api/v1/import-items/${itemID}/resolve`, {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
                'X-CSRF-Token': csrfToken || '',
            },
            body: JSON.stringify({
                action: action,
                sense_refs: senseRefs,
                reason: reason,
                expected_revision: expectedRev,
            }),
        });

        if (!resp.ok) {
            const err = await resp.json().catch(() => ({}));
            alert('操作失败: ' + (err.error?.message || resp.statusText));
            return;
        }

        // Reload current view
        loadItems(importID, currentFilter);
    } catch (e) {
        alert('网络请求失败: ' + e.message);
    }
}

document.addEventListener('DOMContentLoaded', async () => {
    await initClientContext();
    setupUploadPage();
    setupDetailPage();
});
