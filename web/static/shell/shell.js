// Adaptive Vocabulary - Unified Shell JavaScript (M10)

let cachedCSRFToken = null;

export async function getCSRFToken() {
  if (cachedCSRFToken) return cachedCSRFToken;
  try {
    const res = await fetch('/client-context', {
      method: 'GET',
      headers: { 'Accept': 'application/json' },
      cache: 'no-store'
    });
    if (res.ok) {
      const data = await res.json();
      cachedCSRFToken = data.csrf_token;
      window.csrfToken = data.csrf_token;
      return cachedCSRFToken;
    }
  } catch (err) {
    console.warn('Failed to retrieve client context / CSRF token:', err);
  }
  return '';
}

export function showToast(message, type = 'info', duration = 4000) {
  let toast = document.getElementById('shell-toast');
  if (!toast) {
    toast = document.createElement('div');
    toast.id = 'shell-toast';
    toast.className = 'toast-banner hidden';
    document.body.appendChild(toast);
  }

  toast.textContent = message;
  toast.className = `toast-banner ${type}`;
  toast.classList.remove('hidden');

  if (duration > 0) {
    setTimeout(() => {
      toast.classList.add('hidden');
    }, duration);
  }
}

export async function checkMaintenance() {
  try {
    const res = await fetch('/api/v1/maintenance', { cache: 'no-store' });
    if (!res.ok) return null;
    const maint = await res.json();

    const banner = document.getElementById('maintenance-banner');
    const headerBadge = document.getElementById('header-status-badge');
    const systemStatus = document.getElementById('system-status');
    const runtimeMode = document.getElementById('runtime-mode');

    const isMaintenance = maint.mode !== 'active' || maint.requires_rebuild;

    if (banner) {
      if (isMaintenance) {
        banner.textContent = `⚠️ 系统维护中 (${maint.mode})：正在执行数据重算与维护闸门，部分写操作已被安全锁定。建议后续操作: ${maint.next_action || '无'}`;
        banner.classList.add('visible');
      } else {
        banner.classList.remove('visible');
      }
    }

    if (headerBadge) {
      if (isMaintenance) {
        headerBadge.className = 'header-status-badge maintenance';
        headerBadge.title = `系统处于 ${maint.mode} 维护模式`;
      } else {
        headerBadge.className = 'header-status-badge';
        headerBadge.title = '系统就绪';
      }
    }

    if (systemStatus) {
      systemStatus.textContent = isMaintenance ? `维护中 (${maint.mode})` : '服务运行就绪';
    }

    if (runtimeMode) {
      runtimeMode.textContent = maint.mode;
    }

    return maint;
  } catch (err) {
    // Network or server temporarily down
    return null;
  }
}

export async function checkActiveSession() {
  try {
    const res = await fetch('/api/v1/sessions/active', {
      headers: { 'Accept': 'application/json' },
      cache: 'no-store'
    });
    if (!res.ok) return null;
    const data = await res.json();
    const callout = document.getElementById('active-session-callout');
    const textEl = document.getElementById('active-session-text');
    const actionLink = document.getElementById('active-session-action');

    if (data.session && data.session.id) {
      const s = data.session;
      if (callout && textEl && actionLink) {
        const isExam = s.kind === 'exam';
        textEl.textContent = isExam ? '当前有正在进行的阶段考试' : '当前有未完成的自适应练习会话';
        actionLink.href = isExam ? '/exam' : '/practice';
        actionLink.textContent = '继续作答';
        callout.style.display = 'flex';
      }
      return s;
    } else {
      if (callout) {
        callout.style.display = 'none';
      }
      return null;
    }
  } catch (err) {
    return null;
  }
}

export function initShell() {
  // 1. Fetch CSRF token
  getCSRFToken();

  // 2. Initial maintenance check & schedule periodic check
  checkMaintenance();
  setInterval(checkMaintenance, 10000);

  // 3. Check active session
  checkActiveSession();

  // 4. Bind global keyboard shortcuts help
  document.addEventListener('keydown', (e) => {
    if ((e.key === '?' || e.key === 'h' || e.key === 'H') && !['INPUT', 'TEXTAREA'].includes(document.activeElement.tagName)) {
      const shortcuts = document.getElementById('shortcuts-section');
      if (shortcuts) {
        shortcuts.scrollIntoView({ behavior: 'smooth' });
        shortcuts.style.outline = '2px solid var(--primary)';
        setTimeout(() => {
          shortcuts.style.outline = 'none';
        }, 1500);
      }
    }
  });
}

// Automatically init shell on DOM ready
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', initShell);
} else {
  initShell();
}
