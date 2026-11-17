const health = document.getElementById('health');

async function check() {
  try {
    const r = await fetch('/api/health', { headers: { 'Accept': 'application/json' } });
    if (!r.ok) throw new Error('HTTP ' + r.status);
    const d = await r.json();
    health.textContent = '核心已连接 · ' + d.version + ' · 模型模式 ' + d.model_mode;
    health.classList.add('ok');
  } catch (e) {
    health.textContent = '核心未连接：' + e.message;
    health.classList.add('bad');
  }
}
check();
setInterval(check, 5000);
