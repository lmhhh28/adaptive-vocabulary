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
                if (fileInput.files[0].size > 2 * 1024 * 1024) {
                    showBanner('文件大小不能超过 2MB', true);
                    submitBtn.disabled = false;
                    submitBtn.textContent = '开始导入解析';
                    return;
                }
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

        const headerDiv = document.createElement('div');
        headerDiv.className = 'item-header';
        const headerLeftDiv = document.createElement('div');
        const wordSpan = document.createElement('span');
        wordSpan.className = 'item-word';
        wordSpan.textContent = item.normalized_word || item.raw_input;
        headerLeftDiv.appendChild(wordSpan);

        if (item.raw_input !== item.normalized_word) {
            const rawSmall = document.createElement('small');
            rawSmall.className = 'text-muted';
            rawSmall.textContent = ` (原始输入: ${item.raw_input})`;
            headerLeftDiv.appendChild(rawSmall);
        }
        headerDiv.appendChild(headerLeftDiv);

        const statusBadge = document.createElement('span');
        statusBadge.className = `item-status-badge badge-${item.status}`;
        statusBadge.textContent = statusText;
        headerDiv.appendChild(statusBadge);

        card.appendChild(headerDiv);

        const bodyDiv = document.createElement('div');
        bodyDiv.className = 'item-body';

        if (item.disposition_reason) {
            const reasonP = document.createElement('p');
            reasonP.className = 'meta-info';
            reasonP.textContent = `原因: ${item.disposition_reason}`;
            bodyDiv.appendChild(reasonP);
        }

        if (item.candidates && item.candidates.length > 0) {
            const candidatesBox = document.createElement('div');
            candidatesBox.className = 'candidates-box';
            
            const boxTitle = document.createElement('p');
            boxTitle.className = 'text-muted';
            boxTitle.style.marginBottom = '0.4rem';
            boxTitle.style.fontSize = '0.85rem';
            boxTitle.textContent = '候选标准词义 (请选择采纳项):';
            candidatesBox.appendChild(boxTitle);

            item.candidates.forEach((c, idx) => {
                const label = document.createElement('label');
                label.className = 'candidate-option';

                const radio = document.createElement('input');
                radio.type = 'radio';
                radio.name = `candidate-${item.id}`;
                radio.value = c.sense_id;
                if (idx === 0) radio.checked = true;
                if (item.status !== 'ambiguous') radio.disabled = true;
                label.appendChild(radio);

                const infoDiv = document.createElement('div');
                const posSpan = document.createElement('span');
                posSpan.className = 'candidate-pos';
                posSpan.textContent = `[${c.part_of_speech}]`;
                infoDiv.appendChild(posSpan);
                
                infoDiv.appendChild(document.createTextNode(' '));
                
                const meaningSpan = document.createElement('span');
                meaningSpan.className = 'candidate-meaning';
                meaningSpan.textContent = c.meaning;
                infoDiv.appendChild(meaningSpan);

                label.appendChild(infoDiv);
                candidatesBox.appendChild(label);
            });
            bodyDiv.appendChild(candidatesBox);
        }

        if (item.status === 'ambiguous') {
            const actionsDiv = document.createElement('div');
            actionsDiv.className = 'item-actions';
            actionsDiv.innerHTML = `
                <button class="btn btn-primary btn-xs btn-adopt" data-item-id="${item.id}" data-rev="${item.revision}">采纳所选词义</button>
                <button class="btn btn-outline btn-xs btn-exclude" data-item-id="${item.id}" data-rev="${item.revision}">排除此词</button>
            `;
            bodyDiv.appendChild(actionsDiv);
        } else if (item.status === 'not_found') {
            const actionsDiv = document.createElement('div');
            actionsDiv.className = 'item-actions';
            actionsDiv.innerHTML = `
                <button class="btn btn-outline btn-xs btn-exclude" data-item-id="${item.id}" data-rev="${item.revision}">标记排除</button>
            `;
            bodyDiv.appendChild(actionsDiv);
        }

        card.appendChild(bodyDiv);

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
