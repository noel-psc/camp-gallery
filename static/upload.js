(() => {
  'use strict';

  const form = document.getElementById('upload-form');
  if (!form || !window.XMLHttpRequest || !window.FormData) return;

  const feedback = document.getElementById('upload-feedback');
  const status = document.getElementById('upload-status');
  const percent = document.getElementById('upload-percent');
  const progress = document.getElementById('upload-progress');
  const details = document.getElementById('upload-details');
  const button = form.querySelector('button[type="submit"]');
  const controls = Array.from(form.querySelectorAll('input, button'));
  const buttonText = button.textContent;
  let busy = false;

  function formatBytes(bytes) {
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
  }

  form.addEventListener('submit', (event) => {
    event.preventDefault();
    if (busy || !form.reportValidity()) return;

    // Capture the multipart body before disabling controls, which FormData omits.
    const data = new FormData(form);
    const disabled = controls.map((control) => control.disabled);
    const xhr = new XMLHttpRequest();
    busy = true;
    form.setAttribute('aria-busy', 'true');
    controls.forEach((control) => { control.disabled = true; });
    button.textContent = '正在上传…';
    feedback.hidden = false;
    feedback.dataset.state = 'uploading';
    status.textContent = '正在上传，请保持页面打开';
    percent.textContent = '0%';
    progress.value = 0;
    details.textContent = '正在连接服务器…';

    function fail(message) {
      busy = false;
      form.removeAttribute('aria-busy');
      controls.forEach((control, index) => { control.disabled = disabled[index]; });
      button.textContent = buttonText;
      feedback.dataset.state = 'error';
      status.textContent = '上传未完成';
      percent.textContent = '';
      progress.value = 0;
      details.textContent = message;
    }

    function saving() {
      progress.value = 100;
      percent.textContent = '100%';
      feedback.dataset.state = 'saving';
      status.textContent = '文件已发送，正在保存…';
      button.textContent = '正在保存…';
    }

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
    xhr.upload.addEventListener('load', saving);
    xhr.addEventListener('load', () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        status.textContent = '上传成功，正在打开作品…';
        window.location.assign(form.action);
        return;
      }
      let message = '服务器暂时未能完成上传，请稍后查看作品，确认后再重试。';
      if (xhr.status === 413) {
        message = `文件过大，请压缩文件或分批上传后重试（网站单次上限 ${form.dataset.maxUploadMb} MB）。`;
      } else if (xhr.status === 400 || xhr.status === 403) {
        const page = new DOMParser().parseFromString(xhr.responseText, 'text/html');
        message = page.querySelector('p')?.textContent?.trim() || '请核对学号、文件格式及上传截止时间后重试。';
      }
      fail(message);
    });
    xhr.addEventListener('error', () => {
      fail('网络连接中断，请恢复网络后先查看作品，确认是否已上传，再重试。');
    });
    xhr.addEventListener('abort', () => { fail('上传已中止，所选文件已保留，可以重新上传。'); });
    xhr.addEventListener('timeout', () => {
      fail('等待服务器响应超时，请稍后查看作品，确认是否已上传，再重试。');
    });

    try {
      xhr.open('POST', form.action);
      xhr.send(data);
    } catch {
      fail('无法连接服务器，请检查网络后重试。');
    }
  });
})();
