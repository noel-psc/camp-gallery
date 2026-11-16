(() => {
  'use strict';
  const form = document.getElementById('upload-form');
  if (!form || !window.XMLHttpRequest || !window.FormData) return;
  const feedback = document.getElementById('upload-feedback');
  const status = document.getElementById('upload-status');
  const percent = document.getElementById('upload-percent');
  const progress = document.getElementById('upload-progress');
  const details = document.getElementById('upload-details');
  const list = document.getElementById('selected-files');
  const drop = document.getElementById('file-drop');
  const button = form.querySelector('button[type="submit"]');
  const fileInput = form.querySelector('input[type="file"]');
  const requestInput = form.querySelector('input[name="request_id"]');
  const controls = Array.from(form.querySelectorAll('input, textarea, button'));
  const buttonText = button.textContent;
  let busy = false;

  function formatBytes(bytes) {
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
  }
  function changed() {
    if (busy) return;
    requestInput.value = window.crypto?.randomUUID?.() || `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;
    feedback.hidden = true;
    if (!list) return;
    list.replaceChildren();
    const files = Array.from(fileInput.files || []);
    list.hidden = files.length === 0;
    files.forEach((file) => {
      const item = document.createElement('li');
      const name = document.createElement('span');
      const size = document.createElement('span');
      name.className = 'file-name';
      name.textContent = file.name;
      size.className = 'file-size';
      size.textContent = formatBytes(file.size);
      item.append(name, size);
      list.append(item);
    });
  }
  form.addEventListener('input', changed);
  fileInput.addEventListener('change', changed);
  if (drop && window.DataTransfer) {
    ['dragenter', 'dragover'].forEach((type) => drop.addEventListener(type, (event) => {
      event.preventDefault();
      if (!busy) drop.classList.add('drag-over');
    }));
    ['dragleave', 'drop'].forEach((type) => drop.addEventListener(type, (event) => {
      event.preventDefault();
      drop.classList.remove('drag-over');
    }));
    drop.addEventListener('drop', (event) => {
      if (busy || !event.dataTransfer?.files.length) return;
      fileInput.files = event.dataTransfer.files;
      changed();
    });
  }
  form.addEventListener('submit', (event) => {
    event.preventDefault();
    if (busy || !form.reportValidity()) return;
    const data = new FormData(form);
    const article = String(data.get('article') || '').trim();
    const disabled = controls.map((control) => control.disabled);
    const xhr = new XMLHttpRequest();
    function fail(message) {
      busy = false;
      form.removeAttribute('aria-busy');
      controls.forEach((control, index) => { control.disabled = disabled[index]; });
      button.textContent = buttonText;
      feedback.hidden = false;
      feedback.dataset.state = 'error';
      status.textContent = '上传未完成';
      percent.textContent = '';
      progress.value = 0;
      details.textContent = message;
      feedback.scrollIntoView?.({ block: 'nearest' });
    }
    const files = Array.from(fileInput.files || []);
    if (!files.length && !article) {
      fail('请选择作品文件或粘贴文章正文。');
      return;
    }
    if (Array.from(article).length > 20000) {
      fail('文章正文最多 20000 字，请精简后提交或上传 DOCX。');
      return;
    }
    if (files.length > 100 || files.reduce((sum, file) => sum + file.size, 0) > Number(form.dataset.maxUploadMb) * 1024 * 1024) {
      fail(`请分批上传：每次最多 100 个文件，合计不超过 ${form.dataset.maxUploadMb} MB。`);
      return;
    }
    busy = true;
    form.setAttribute('aria-busy', 'true');
    controls.forEach((control) => { control.disabled = true; });
    button.textContent = '正在上传…';
    feedback.hidden = false;
    feedback.dataset.state = 'uploading';
    status.textContent = '正在上传，请保持页面打开';
    percent.textContent = '0%';
    progress.value = 0;
    details.textContent = '正在连接服务器，繁忙时可能需要等待…';
    feedback.scrollIntoView?.({ block: 'nearest' });
    xhr.upload.addEventListener('progress', (event) => {
      if (event.lengthComputable && event.total > 0) {
        const value = Math.min(100, Math.floor(event.loaded / event.total * 100));
        progress.value = value;
        percent.textContent = `${value}%`;
        details.textContent = `${formatBytes(event.loaded)} / ${formatBytes(event.total)}`;
      } else {
        progress.removeAttribute('value');
        percent.textContent = '';
        details.textContent = `已发送 ${formatBytes(event.loaded)}`;
      }
    });
    xhr.upload.addEventListener('load', () => {
      progress.value = 100;
      percent.textContent = '100%';
      feedback.dataset.state = 'saving';
      status.textContent = '文件已发送，正在保存…';
      button.textContent = '正在保存…';
    });
    xhr.addEventListener('load', () => {
      let response;
      try { response = JSON.parse(xhr.responseText); } catch { /* A proxy may return an HTML error page. */ }
      if (xhr.status >= 200 && xhr.status < 300 && response?.ok === true) {
        status.textContent = '上传成功，正在打开作品…';
        window.location.assign(form.action);
        return;
      }
      let message = '服务器暂时未能完成上传，请查看作品确认后再重试。';
      if (xhr.status === 413) message = `文件过大，请压缩或分批上传（网站单次上限 ${form.dataset.maxUploadMb} MB）。`;
      if (xhr.status === 429) message = '当前上传人数较多，请稍候再试，所选文件已保留。';
      if (xhr.status === 507) message = '存储空间不足，请联系活动老师，所选文件已保留。';
      if (typeof response?.error === 'string') message = response.error;
      fail(message);
    });
    xhr.addEventListener('error', () => fail('网络连接中断，请恢复网络后重试；相同内容的重试会自动核对，避免重复作品。'));
    xhr.addEventListener('abort', () => fail('上传已中止，所选文件已保留，可以重新上传。'));
    xhr.addEventListener('timeout', () => fail('上传等待超时，请检查网络后重试，所选文件已保留。'));
    try {
      xhr.open('POST', form.action);
      xhr.timeout = Number(form.dataset.timeoutMs) || 31 * 60 * 1000;
      xhr.setRequestHeader('Accept', 'application/json');
      xhr.setRequestHeader('X-Upload-ID', data.get('request_id'));
      xhr.send(data);
    } catch {
      fail('无法连接服务器，请检查网络后重试。');
    }
  });
})();
