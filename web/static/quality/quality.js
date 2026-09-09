// Quality management frontend script

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

let csrfToken = null;

async function fetchClientContext() {
  try {
    const resp = await fetch('/client-context');
    if (resp.ok) {
      const data = await resp.json();
      csrfToken = data.csrf_token;
    }
  } catch (err) {
    console.error('Failed to fetch client context:', err);
  }
}

document.addEventListener('DOMContentLoaded', async () => {
  await fetchClientContext();

  const form = document.getElementById('disposition-form');
  const questionSelect = document.getElementById('question-select');
  const actionSelect = document.getElementById('action-select');
  const reasonInput = document.getElementById('reason-input');
  const feedbackEl = document.getElementById('action-feedback');
  const submitBtn = document.getElementById('btn-submit-disposition');

  if (!form) return;

  form.addEventListener('submit', async (e) => {
    e.preventDefault();

    const versionID = questionSelect.value;
    if (!versionID) {
      showFeedback('请选择一个题目版本', 'error');
      return;
    }

    const selectedOption = questionSelect.options[questionSelect.selectedIndex];
    const expectedRevisionStr = selectedOption.getAttribute('data-revision');
    const expectedRevision = expectedRevisionStr ? parseInt(expectedRevisionStr, 10) : undefined;

    const action = actionSelect.value;
    const reason = reasonInput.value.trim();

    if (!reason) {
      showFeedback('请输入处置原因', 'error');
      return;
    }

    if (!csrfToken) {
      await fetchClientContext();
    }

    submitBtn.disabled = true;
    submitBtn.textContent = '提交处理中...';

    try {
      const idempotencyKey = generateUUID();
      const resp = await fetch(`/api/v1/question-versions/${versionID}/evidence-dispositions`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'X-CSRF-Token': csrfToken || '',
          'Idempotency-Key': idempotencyKey,
        },
        body: JSON.stringify({
          action: action,
          reason: reason,
          expected_revision: expectedRevision,
        }),
      });

      const data = await resp.json();

      if (!resp.ok) {
        const errMsg = (data.error && data.error.message) ? data.error.message : '处置失败';
        const errCode = (data.error && data.error.code) ? data.error.code : 'ERROR';
        showFeedback(`[${errCode}] ${errMsg}`, 'error');
        submitBtn.disabled = false;
        submitBtn.textContent = '提交处置并触发重建';
        return;
      }

      showFeedback(`处置成功！处置ID: ${data.disposition_id}，重建状态: ${data.status}。页面即将刷新...`, 'success');
      setTimeout(() => {
        window.location.reload();
      }, 1200);
    } catch (err) {
      showFeedback(`网络或请求异常: ${err.message}`, 'error');
      submitBtn.disabled = false;
      submitBtn.textContent = '提交处置并触发重建';
    }
  });

  function showFeedback(msg, type) {
    feedbackEl.textContent = msg;
    feedbackEl.className = `feedback-msg ${type}`;
  }
});
