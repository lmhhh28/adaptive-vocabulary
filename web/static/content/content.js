// Native ES Module for Question Bank interactions

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

document.addEventListener('DOMContentLoaded', () => {
    // 1. Create Bank Modal Logic
    const btnShowModal = document.getElementById('btn-show-create-modal');
    const modal = document.getElementById('create-bank-modal');
    const btnCloseModal = document.getElementById('btn-close-create-modal');
    const btnCancelModal = document.getElementById('btn-cancel-create');
    const createForm = document.getElementById('create-bank-form');
    const createErrorMsg = document.getElementById('create-error-msg');

    if (btnShowModal && modal) {
        btnShowModal.addEventListener('click', () => {
            modal.classList.remove('hidden');
            document.getElementById('bank-name-input').focus();
        });

        const closeModal = () => {
            modal.classList.add('hidden');
            createErrorMsg.classList.add('hidden');
            createForm.reset();
        };

        if (btnCloseModal) btnCloseModal.addEventListener('click', closeModal);
        if (btnCancelModal) btnCancelModal.addEventListener('click', closeModal);

        createForm.addEventListener('submit', async (e) => {
            e.preventDefault();
            createErrorMsg.classList.add('hidden');

            const name = document.getElementById('bank-name-input').value.trim();
            const note = document.getElementById('bank-note-input').value.trim();
            const csrfToken = await getCSRFToken();

            try {
                const res = await fetch('/api/v1/banks', {
                    method: 'POST',
                    headers: {
                        'Content-Type': 'application/json',
                        'X-CSRF-Token': csrfToken,
                        'Idempotency-Key': generateUUID()
                    },
                    body: JSON.stringify({ name, note })
                });

                if (res.ok) {
                    window.location.reload();
                } else {
                    const err = await res.json();
                    createErrorMsg.textContent = err.error?.message || '创建题库失败';
                    createErrorMsg.classList.remove('hidden');
                }
            } catch (err) {
                createErrorMsg.textContent = '网络错误，请稍后重试';
                createErrorMsg.classList.remove('hidden');
            }
        });
    }

    // 2. Member Removal Logic
    const membersTable = document.getElementById('members-table');
    const operationMsg = document.getElementById('operation-msg');
    const headerEl = document.querySelector('.bank-detail-header');

    if (membersTable && headerEl) {
        const bankID = headerEl.dataset.bankId;

        membersTable.addEventListener('click', async (e) => {
            if (!e.target.classList.contains('btn-remove-member')) return;
            const btn = e.target;
            const senseID = btn.dataset.senseId;
            const row = btn.closest('tr');
            const currentRevision = parseInt(headerEl.dataset.revision, 10);

            if (!confirm('确认从当前题库移除该词义？（全局词义和历史作答不受影响）')) return;

            const csrfToken = await getCSRFToken();
            try {
                const res = await fetch(`/api/v1/banks/${bankID}/members/remove`, {
                    method: 'POST',
                    headers: {
                        'Content-Type': 'application/json',
                        'X-CSRF-Token': csrfToken,
                        'Idempotency-Key': generateUUID()
                    },
                    body: JSON.stringify({
                        sense_ids: [senseID],
                        expected_revision: currentRevision
                    })
                });

                if (res.ok) {
                    const data = await res.json();
                    headerEl.dataset.revision = data.revision;
                    const revEl = document.getElementById('bank-revision-val');
                    if (revEl) revEl.textContent = data.revision;

                    row.remove();
                    const countEl = document.getElementById('member-count-val');
                    if (countEl) countEl.textContent = Math.max(0, parseInt(countEl.textContent, 10) - 1);
                } else {
                    const err = await res.json();
                    if (operationMsg) {
                        operationMsg.textContent = err.error?.message || '移除成员失败';
                        operationMsg.classList.remove('hidden');
                    }
                }
            } catch (err) {
                if (operationMsg) {
                    operationMsg.textContent = '网络错误，请稍后重试';
                    operationMsg.classList.remove('hidden');
                }
            }
        });

        // 3. Bank Archiving Logic
        const btnArchive = document.getElementById('btn-archive-bank');
        if (btnArchive) {
            btnArchive.addEventListener('click', async () => {
                const currentRevision = parseInt(headerEl.dataset.revision, 10);
                if (!confirm('确认归档当前题库？')) return;

                const csrfToken = await getCSRFToken();
                try {
                    const res = await fetch(`/api/v1/banks/${bankID}/archive`, {
                        method: 'POST',
                        headers: {
                            'Content-Type': 'application/json',
                            'X-CSRF-Token': csrfToken,
                            'Idempotency-Key': generateUUID()
                        },
                        body: JSON.stringify({
                            expected_revision: currentRevision
                        })
                    });

                    if (res.ok) {
                        window.location.reload();
                    } else {
                        const err = await res.json();
                        if (operationMsg) {
                            operationMsg.textContent = err.error?.message || '归档题库失败';
                            operationMsg.classList.remove('hidden');
                        }
                    }
                } catch (err) {
                    if (operationMsg) {
                        operationMsg.textContent = '网络错误，请稍后重试';
                        operationMsg.classList.remove('hidden');
                    }
                }
            });
        }
    }
});
